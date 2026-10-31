package einvoice_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dcnws/invoicekit.go"
)

// refDir is the checkout of the official CEF EN 16931 validation artefacts,
// used here only to exercise the public API against real documents. Override
// it with EINVOICE_REF; the test is skipped when the corpus is absent.
func refDir(t *testing.T) string {
	t.Helper()
	d := os.Getenv("EINVOICE_REF")
	if d == "" {
		d = "../ref-en16931"
	}
	if _, err := os.Stat(d); err != nil {
		t.Skipf("reference corpus not available at %s", d)
	}
	return d
}

func read(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return data
}

func TestDetectAndValidateUBL(t *testing.T) {
	dir := filepath.Join(refDir(t), "test", "testfiles")
	files, _ := filepath.Glob(filepath.Join(dir, "*.xml"))
	if len(files) == 0 {
		t.Fatalf("no reference invoices in %s", dir)
	}
	for _, f := range files {
		data := read(t, f)
		sx, err := einvoice.Detect(data)
		if err != nil {
			t.Errorf("Detect(%s): %v", f, err)
			continue
		}
		if sx != einvoice.SyntaxUBL {
			t.Errorf("Detect(%s) = %q, want UBL", f, sx)
		}
		res, err := einvoice.Validate(data, einvoice.Options{})
		if err != nil {
			t.Errorf("Validate(%s): %v", f, err)
			continue
		}
		if !res.Valid {
			t.Errorf("%s: %d findings, want none: %v", f, len(res.Findings), res.Findings[:min(5, len(res.Findings))])
		}
		if got := res.IDs(); len(got) != 0 {
			t.Errorf("%s: fired %v, want no rules", f, got)
		}
	}
}

func TestDetectAndValidateCII(t *testing.T) {
	dir := filepath.Join(refDir(t), "cii", "examples")
	files, _ := filepath.Glob(filepath.Join(dir, "*.xml"))
	if len(files) == 0 {
		t.Fatalf("no reference invoices in %s", dir)
	}
	for _, f := range files {
		data := read(t, f)
		sx, err := einvoice.Detect(data)
		if err != nil {
			t.Errorf("Detect(%s): %v", f, err)
			continue
		}
		if sx != einvoice.SyntaxCII {
			t.Errorf("Detect(%s) = %q, want CII", f, sx)
		}
		res, err := einvoice.Validate(data, einvoice.Options{Syntax: einvoice.SyntaxCII})
		if err != nil {
			t.Errorf("Validate(%s): %v", f, err)
			continue
		}
		if !res.Valid {
			t.Errorf("%s: %d findings, want none: %v", f, len(res.Findings), res.Findings[:min(5, len(res.Findings))])
		}
	}
}

// TestInvalidDocument checks that the public surface reports the rules a real
// broken invoice should trip, and that the derived helpers agree.
func TestInvalidDocument(t *testing.T) {
	path := filepath.Join(refDir(t), "test", "testfiles", "Invoice-Min_content_with_VAT.xml")
	doc := string(read(t, path))
	const needle = "<cbc:ID>1</cbc:ID>" // first line id: BR-21 must fire
	if !strings.Contains(doc, needle) {
		t.Fatalf("fixture %s has changed", path)
	}
	doc = strings.Replace(doc, needle, "", 1)

	res, err := einvoice.Validate([]byte(doc), einvoice.Options{Syntax: einvoice.SyntaxUBL})
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if res.Valid {
		t.Fatal("broken invoice reported valid")
	}
	if res.Syntax != einvoice.SyntaxUBL {
		t.Errorf("Syntax = %q, want UBL", res.Syntax)
	}
	counts := res.Counts()
	if counts["BR-21"] == 0 {
		t.Errorf("BR-21 did not fire, fired rules: %v", res.IDs())
	}
	for _, f := range res.Findings {
		if f.ID == "" || f.Message == "" {
			t.Errorf("finding missing id or message: %+v", f)
		}
	}
	if res.Fatal != len(res.Findings) {
		t.Errorf("Fatal = %d, want %d", res.Fatal, len(res.Findings))
	}

	ids, err := einvoice.ValidateFile([]byte(doc))
	if err != nil {
		t.Fatalf("ValidateFile: %v", err)
	}
	if len(ids) == 0 {
		t.Error("ValidateFile returned no rule ids")
	}
}

func TestPhases(t *testing.T) {
	for _, sx := range []einvoice.Syntax{einvoice.SyntaxCII, einvoice.SyntaxUBL} {
		ids, err := einvoice.Phases(sx)
		if err != nil {
			t.Fatalf("Phases(%s): %v", sx, err)
		}
		if len(ids) < 2 {
			t.Errorf("Phases(%s) = %v, want at least a model and a codelist phase", sx, ids)
		}
	}
}

func TestValidateNonXML(t *testing.T) {
	if _, err := einvoice.Detect([]byte("hello")); err == nil {
		t.Error("Detect on plain text should fail")
	}
	if _, err := einvoice.Validate([]byte("hello"), einvoice.Options{}); err == nil {
		t.Error("Validate on plain text should fail")
	}
	if _, err := einvoice.Validate([]byte("<not-an-invoice/>"), einvoice.Options{}); err == nil {
		t.Error("Validate on an unknown root element should fail")
	}
}

// TestProfiles checks the public surface for published rule-set bundles: what
// the library advertises, what a run reports back, and that a bundle carries
// its own copy of the norm rather than doubling up with ours.
func TestProfiles(t *testing.T) {
	names := einvoice.ProfileNames()
	if len(names) == 0 {
		t.Fatal("no rule-set bundles are embedded")
	}
	var peppol bool
	for _, n := range names {
		if einvoice.ProfileVersion(einvoice.Profile(n)) == "" {
			t.Errorf("ProfileVersion(%q) is empty", n)
		}
		if n == string(einvoice.ProfilePeppol) {
			peppol = true
		}
	}
	if !peppol {
		t.Fatalf("ProfileNames() = %v, want it to list %q", names, einvoice.ProfilePeppol)
	}

	data := read(t, "testdata/samples/ubl-peppol.xml")
	plain, err := einvoice.Validate(data, einvoice.Options{})
	if err != nil {
		t.Fatal(err)
	}
	rep, err := einvoice.Validate(data, einvoice.Options{Profile: einvoice.ProfilePeppol})
	if err != nil {
		t.Fatal(err)
	}
	if rep.Profile != einvoice.ProfilePeppol {
		t.Errorf("Result.Profile = %q, want %q", rep.Profile, einvoice.ProfilePeppol)
	}
	if rep.CIUS != "" {
		t.Errorf("Result.CIUS = %q, want empty", rep.CIUS)
	}
	if !rep.Valid {
		t.Errorf("the Peppol example broke the network rules: %v", rep.IDs())
	}
	for _, f := range rep.Findings {
		if f.Source != "peppol" && f.Source != "peppol-cen" {
			t.Errorf("%s: Source = %q, want the bundle's own artefacts", f.ID, f.Source)
		}
	}
	// Every finding says which artefact spoke, so a mixed run stays readable.
	if len(rep.Findings) == 0 && len(plain.Findings) == 0 {
		t.Log("the example passes both our artefacts and the bundle's")
	}
	// IDs() and Counts() have to work on a profile run too, since they rebuild
	// the internal report from the result.
	if len(rep.IDs()) != 0 || len(rep.Counts()) != 0 {
		t.Errorf("a clean run reported %v", rep.IDs())
	}
}

// TestProfileCIUSStack checks that a CIUS runs on top of a bundle: the profile
// replaces the EN 16931 artefacts it ships, but a country segment is still an
// addition, whoever published the base.
func TestProfileCIUSStack(t *testing.T) {
	data := read(t, "testdata/samples/ubl-peppol.xml")
	rep, err := einvoice.Validate(data, einvoice.Options{
		Profile: einvoice.ProfilePeppol,
		CIUS:    einvoice.CIUSXRechnung,
	})
	if err != nil {
		t.Fatal(err)
	}
	if rep.Profile != einvoice.ProfilePeppol || rep.CIUS != einvoice.CIUSXRechnung {
		t.Errorf("Result says profile=%q cius=%q, want %q and %q",
			rep.Profile, rep.CIUS, einvoice.ProfilePeppol, einvoice.CIUSXRechnung)
	}
	var german int
	for _, f := range rep.Findings {
		if f.Source != "xrechnung" {
			continue
		}
		german++
	}
	if german == 0 {
		t.Errorf("the CIUS contributed no findings to %v, which is what a stack must do", rep.IDs())
	}
}

// TestProfileErrors checks the two ways a caller gets a profile wrong: an
// unknown name, and phases that belong to no bundle.
func TestProfileErrors(t *testing.T) {
	if _, err := einvoice.Validate(read(t, "testdata/samples/ubl-peppol.xml"),
		einvoice.Options{Profile: "peppol-bis"}); err == nil ||
		!strings.Contains(err.Error(), string(einvoice.ProfilePeppol)) {
		t.Errorf("unknown bundle error = %v, want it to name what is available", err)
	}
	if _, err := einvoice.ProfilePhases("peppol-bis", einvoice.SyntaxUBL); err == nil {
		t.Error("ProfilePhases of an unknown bundle should fail")
	}
	ids, err := einvoice.ProfilePhases(einvoice.ProfilePeppol, einvoice.SyntaxUBL)
	if err != nil {
		t.Fatal(err)
	}
	if len(ids) == 0 {
		t.Error("the bundle declares no phases, which its EN 16931 artefact should")
	}
	if v := einvoice.ProfileVersion("nope"); v != "" {
		t.Errorf("ProfileVersion of an unknown bundle = %q, want empty", v)
	}
}
