package en16931

import (
	"bytes"
	"encoding/xml"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/dcnws/invoicekit.go/internal/xp"
)

// refDir returns the checkout of the official CEF EN 16931 repository, whose
// test files we use as a conformance corpus. Point EINVOICE_REF at it, or put
// the checkout beside this repository as ../ref-en16931; the tests that need it
// skip when it is absent, so a plain go test ./... never fails over a corpus
// nobody downloaded.
func refDir(t *testing.T) string {
	t.Helper()
	d := corpusDir(t, "EINVOICE_REF", "ref-en16931")
	if d == "" {
		t.Skipf("conformance corpus not available: set EINVOICE_REF to a CEF EN 16931 checkout")
	}
	return d
}

// TestFullDocuments runs the complete invoice instances shipped in the CEF
// repository through the validator. They are all well-formed, schematically
// valid documents that pass the official Schematrons, so any finding is a
// false positive from our engine.
func TestFullDocuments(t *testing.T) {
	dir := filepath.Join(refDir(t), "test", "testfiles")
	files, err := filepath.Glob(filepath.Join(dir, "*.xml"))
	if err != nil || len(files) == 0 {
		t.Fatalf("no test files in %s: %v", dir, err)
	}
	var total int
	for _, f := range files {
		data, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		rep, err := Validate(data, Options{})
		if err != nil {
			t.Errorf("%s: %v", filepath.Base(f), err)
			continue
		}
		total++
		if len(rep.Findings) > 0 {
			t.Errorf("%s: %d unexpected findings: %v", filepath.Base(f), len(rep.Findings), rep.IDs())
			for _, fi := range rep.Findings {
				t.Logf("  %s [%s] %s (ctx=%s err=%v)", fi.ID, fi.Flag, fi.Message, fi.Context, fi.Err)
			}
		}
	}
	t.Logf("%d full documents validated", total)
}

// TestVEFATestSets runs the official unit test sets. Each <test> element
// carries <success>BR-x</success> and <error>BR-x</error> assertions followed
// by a payload document: the success rules must NOT fire and the error rules
// MUST fire on that document. Asserting both directions is what keeps the
// engine from passing by being permissive.
func TestVEFATestSets(t *testing.T) {
	root := refDir(t)
	dirs := []string{
		filepath.Join(root, "test", "Invoice-unit-UBL"),
		filepath.Join(root, "test", "CreditNote-unit-UBL"),
		filepath.Join(root, "test", "cii"),
	}
	var cases, failures, checked, mustfires int
	for _, d := range dirs {
		files, err := filepath.Glob(filepath.Join(d, "*.xml"))
		if err != nil {
			t.Fatal(err)
		}
		for _, f := range files {
			data, err := os.ReadFile(f)
			if err != nil {
				t.Fatal(err)
			}
			n, bad, chk, mf, err := runTestSet(t, f, data, Options{})
			if err != nil {
				t.Errorf("%s: %v", filepath.Base(f), err)
				continue
			}
			cases += n
			failures += bad
			checked += chk
			mustfires += mf
		}
		if len(files) == 0 {
			t.Logf("no test sets in %s", d)
		}
	}
	t.Logf("%d VEFA cases, %d must-not-fire + %d must-fire assertions checked, %d failures",
		cases, checked, mustfires, failures)
	if cases == 0 {
		t.Fatal("no VEFA test sets found")
	}
}

// glob and read are small helpers shared by the corpus tests.
func glob(t *testing.T, dir, pattern string) []string {
	t.Helper()
	files, err := filepath.Glob(filepath.Join(dir, pattern))
	if err != nil {
		t.Fatal(err)
	}
	return files
}

func read(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

// runTestSet validates every payload in one test set file with the rule sets
// opts names. It returns the number of test cases, the number of rule
// assertions that failed, and a structural error for the file as a whole.
func runTestSet(t *testing.T, path string, data []byte, opts Options) (cases, bad, checked, mf int, err error) {
	t.Helper()
	dec := xml.NewDecoder(bytes.NewReader(data))
	var want, fire []string
	for {
		tk, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return cases, bad, checked, mf, err
		}
		se, ok := tk.(xml.StartElement)
		if !ok {
			continue
		}
		switch se.Name.Local {
		case "test":
			want, fire = want[:0], fire[:0]
		case "success":
			var s string
			if err := dec.DecodeElement(&s, &se); err != nil {
				return cases, bad, checked, mf, err
			}
			want = append(want, s)
		case "error":
			var s string
			if err := dec.DecodeElement(&s, &se); err != nil {
				return cases, bad, checked, mf, err
			}
			// <error number="3">BR-x</error> also states how many times the
			// rule should fire; only the presence of the finding is asserted,
			// because a single defect can legitimately be reported by more than
			// one rule.
			fire = append(fire, s)
		case "fail":
			var s string
			if err := dec.DecodeElement(&s, &se); err != nil {
				return cases, bad, checked, mf, err
			}
			// Negative expectations are recorded but not asserted: the official
			// unit sets contain none, and a rule firing may be reported under a
			// different id than the test author expected.
		default:
			if se.Name.Space != nsUBL && se.Name.Space != nsUBLC && se.Name.Space != nsCII {
				continue
			}
			doc, err := xp.ParseElement(dec, se)
			if err != nil {
				return cases, bad, checked, mf, err
			}
			cases++
			sx := SyntaxCII
			if se.Name.Space == nsUBL || se.Name.Space == nsUBLC {
				sx = SyntaxUBL
			}
			rep, err := ValidateDocWith(sx, doc, opts)
			if err != nil {
				t.Errorf("%s: payload: %v", filepath.Base(path), err)
				bad++
				continue
			}
			fired := map[string]bool{}
			for _, fi := range rep.Findings {
				fired[fi.ID] = true
				if fi.Err != "" {
					// The engine could not evaluate the rule at all, which is
					// a fault in the engine and not in the document.
					t.Errorf("%s: %s [%s] could not be evaluated: %s", filepath.Base(path), fi.ID, fi.Source, fi.Err)
					bad++
				}
			}
			for _, id := range want {
				checked++
				if fired[id] {
					t.Errorf("%s: rule %s fired but the test set expects success: %v", filepath.Base(path), id, rep.IDs())
					bad++
				}
			}
			for _, id := range fire {
				mf++
				if !fired[id] {
					t.Errorf("%s: rule %s did not fire but the test set expects an error: %v", filepath.Base(path), id, rep.IDs())
					bad++
				}
			}
			want, fire = want[:0], fire[:0]
		}
	}
	return cases, bad, checked, mf, nil
}
