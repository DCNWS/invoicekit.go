package main

import (
	"errors"
	"flag"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/dcnws/invoicekit.go/internal/en16931"
)

// sampleDir is absolute, so tests that change the working directory still find
// the repository's invoice samples.
var sampleDir = mustAbs(filepath.Join("..", "..", "testdata", "samples"))

func mustAbs(p string) string {
	a, err := filepath.Abs(p)
	if err != nil {
		panic(err)
	}
	return a
}

// copySample puts one of the repository's invoice samples where a test needs
// it, so a fake reference checkout holds documents the engine really accepts.
func copySample(t *testing.T, name, dir string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(sampleDir, name))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	to := filepath.Join(dir, name)
	if err := os.WriteFile(to, b, 0o644); err != nil {
		t.Fatal(err)
	}
	return to
}

// fakeRef writes a miniature of the CEF reference checkout: the three test-set
// directories, and two example directories holding clean documents. Only the
// shape matters to the pass; the real checkout is 1,700 files.
func fakeRef(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	for _, d := range []string{"test/Invoice-unit-UBL", "test/CreditNote-unit-UBL", "test/cii", "test/testfiles", "cii/examples"} {
		if err := os.MkdirAll(filepath.Join(root, filepath.FromSlash(d)), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	// The payload carries a four letter currency code, so it fires rules and
	// proves them the way the real test sets' failing documents do. A payload
	// that validated clean would prove nothing at all.
	flawed := strings.Replace(string(mustRead(t, filepath.Join(sampleDir, "ubl-min.xml"))),
		"<cbc:DocumentCurrencyCode>SEK</cbc:DocumentCurrencyCode>",
		"<cbc:DocumentCurrencyCode>XYZ</cbc:DocumentCurrencyCode>", 1)
	testSet := `<test suite="local">
<error>BR-DOES-NOT-FIRE</error>
<error>BR-CL-04</error>
<success>BR-03</success>
` + strings.TrimSpace(flawed) + `
</test>
`
	if err := os.WriteFile(filepath.Join(root, "test", "Invoice-unit-UBL", "one.xml"), []byte(testSet), 0o644); err != nil {
		t.Fatal(err)
	}
	copySample(t, "ubl-min.xml", filepath.Join(root, "test", "testfiles"))
	copySample(t, "cii-xrechnung.xml", filepath.Join(root, "cii", "examples"))
	return root
}

func mustRead(t *testing.T, path string) []byte {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestParseFlags(t *testing.T) {
	_, errOut := captureStreams(t)
	cfg, err := parseFlags([]string{"-ref", "refs", "-corpus", "corpus", "-json", "s.json",
		"-report", "r.md", "-bases", "a.xml,b.xml", "-limit", "3", "-no-mutate", "-targeted", "-jobs", "4"})
	if err != nil {
		t.Fatal(err)
	}
	want := config{ref: "refs", corpus: "corpus", jsonOut: "s.json", report: "r.md",
		bases: "a.xml,b.xml", limit: 3, noMutate: true, targeted: true, jobs: 4}
	if cfg != want {
		t.Errorf("parsed %+v, want %+v", cfg, want)
	}

	def, err := parseFlags(nil)
	if err != nil {
		t.Fatal(err)
	}
	if def.corpus != "testdata/mutations" || def.limit != 0 || def.noMutate || def.targeted {
		t.Errorf("defaults = %+v", def)
	}
	if def.ref != envOr("EINVOICE_REF", "../ref-en16931") {
		t.Errorf("ref default = %q", def.ref)
	}

	if _, err := parseFlags([]string{"-limit", "not-a-number"}); err == nil {
		t.Error("a non-numeric -limit should be refused")
	}
	if _, err := parseFlags([]string{"-h"}); !errors.Is(err, flag.ErrHelp) {
		t.Errorf("-h = %v, want flag.ErrHelp", err)
	}
	if !strings.Contains(errOut.String(), "-limit") {
		t.Errorf("usage went nowhere:\n%s", errOut.String())
	}
}

func TestRunDrivesTheWholePass(t *testing.T) {
	out, errOut := captureStreams(t)
	dir := t.TempDir()
	base := copySample(t, "ubl-min.xml", filepath.Join(dir, "bases"))
	jsonPath, mdPath := filepath.Join(dir, "summary.json"), filepath.Join(dir, "report.md")

	cfg, err := parseFlags([]string{"-ref", fakeRef(t), "-corpus", filepath.Join(dir, "corpus"),
		"-bases", base, "-no-mutate", "-jobs", "2", "-json", jsonPath, "-report", mdPath})
	if err != nil {
		t.Fatal(err)
	}
	if err := run(cfg); err != nil {
		t.Fatal(err)
	}

	if _, err := os.ReadFile(jsonPath); err != nil {
		t.Errorf("no machine summary: %v", err)
	}
	md, err := os.ReadFile(mdPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(md), "## UBL:") {
		t.Errorf("report has no UBL section:\n%s", md)
	}
	for _, want := range []string{
		"corpus replay: 0 mutants",
		"UBL: pattern UBL-syntax",
		"1 clean bases, 0 mutants",
		"official test-set payloads: 1, must-fire expectations: 2, not fired: 1",
		// two asserts proven by the official test set, out of every UBL assert
		"UBL: 2/",
	} {
		got := out.String() + errOut.String()
		if !strings.Contains(got, want) {
			t.Errorf("run said nothing about %q\nstdout:\n%s\nstderr:\n%s", want, out.String(), errOut.String())
		}
	}
	// A proven-by-testset assert must be recorded in the corpus manifest, which
	// is what makes the next run's replay prove the same thing.
	man := mustRead(t, filepath.Join(dir, "corpus", "UBL", "manifest.json"))
	if !strings.Contains(string(man), "official-testset") {
		t.Errorf("manifest does not name the pass that proved the assert: %s", man)
	}
}

func TestRunRefusesUnusableOutputAndBases(t *testing.T) {
	// A coverage run that cannot write what it was told to write, or cannot read
	// a base it was told to mutate, must say so rather than report a pass that
	// quietly did half the job.
	for _, tc := range []struct {
		name string
		args []string
		want string
	}{
		{"summary outside the workspace", []string{"-json", "gone/summary.json"}, "gone/summary.json"},
		{"report outside the workspace", []string{"-report", "gone/report.md"}, "gone/report.md"},
		{"a missing base document", []string{"-bases", "gone.xml"}, "gone.xml"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			captureStreams(t)
			dir := t.TempDir()
			args := append([]string{"-ref", fakeRef(t), "-corpus", filepath.Join(dir, "corpus"),
				"-no-mutate", "-jobs", "2"}, tc.args...)
			cfg, err := parseFlags(args)
			if err != nil {
				t.Fatal(err)
			}
			t.Chdir(dir)
			if err := run(cfg); err == nil {
				t.Fatal("run reported success")
			} else if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error %q does not mention %q", err, tc.want)
			}
		})
	}
}

func TestMainReportsUsageMistakes(t *testing.T) {
	_, errOut := captureStreams(t)
	var codes []int
	prevExit, prevArgs := exit, os.Args
	exit = func(c int) { codes = append(codes, c) }
	t.Cleanup(func() { exit, os.Args = prevExit, prevArgs })

	os.Args = []string{"cov", "-h"}
	main()
	if codes[len(codes)-1] != 0 {
		t.Errorf("-h exited %d, want 0", codes[len(codes)-1])
	}
	os.Args = []string{"cov", "-nope"}
	main()
	if codes[len(codes)-1] != 2 {
		t.Errorf("an unknown flag exited %d, want 2", codes[len(codes)-1])
	}
	if !strings.Contains(errOut.String(), "flag provided but not defined") {
		t.Errorf("the mistake was not explained:\n%s", errOut.String())
	}
}

func TestMainFailsOnAnUnusableCommandLine(t *testing.T) {
	_, errOut := captureStreams(t)
	var codes []int
	prevExit, prevArgs := exit, os.Args
	exit = func(c int) { codes = append(codes, c) }
	t.Cleanup(func() { exit, os.Args = prevExit, prevArgs })

	os.Args = []string{"cov", "-no-mutate", "-ref", filepath.Join(t.TempDir(), "absent")}
	main()
	if codes[len(codes)-1] != 1 {
		t.Errorf("exit %d, want 1 when there is nothing to read", codes[len(codes)-1])
	}
	if !strings.Contains(errOut.String(), "cov:") {
		t.Errorf("no failure line:\n%s", errOut.String())
	}
}

func TestTestSetsReadsExpectationsAndPayloads(t *testing.T) {
	dir := t.TempDir()
	doc := strings.TrimSpace(string(mustRead(t, filepath.Join(sampleDir, "cii-xrechnung.xml"))))
	body := `<test>
<error>BR-01</error>
<success>BR-02</success>
` + doc + `
</test>
<test>
<error>BR-03</error>
` + doc + `
</test>
`
	if err := os.WriteFile(filepath.Join(dir, "tests.xml"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	pts, err := testSets([]string{dir, filepath.Join(dir, "no-such-dir")})
	if err != nil {
		t.Fatal(err)
	}
	if len(pts) != 2 {
		t.Fatalf("got %d payloads, want 2", len(pts))
	}
	if pts[0].sy != en16931.SyntaxCII || pts[0].idx != 1 {
		t.Errorf("first payload = %s #%d", pts[0].sy, pts[0].idx)
	}
	if strings.Join(pts[0].mustFire, ",") != "BR-01" || strings.Join(pts[0].mustOK, ",") != "BR-02" {
		t.Errorf("expectations = %v / %v", pts[0].mustFire, pts[0].mustOK)
	}
	if strings.Join(pts[1].mustOK, ",") != "" || strings.Join(pts[1].mustFire, ",") != "BR-03" {
		t.Errorf("second test block leaked expectations: %v / %v", pts[1].mustFire, pts[1].mustOK)
	}
}

func TestTestSetsRefusesBrokenTestFiles(t *testing.T) {
	broken := map[string]string{
		"truncated.xml":  `<test><CrossIndustryInvoice><a></test>`,
		"unclosed.xml":   `<test><error>BR-01</error></test`,
		"badpayload.xml": "<test><CrossIndustryInvoice><a><b></CrossIndustryInvoice></test>",
	}
	for name, body := range broken {
		// One directory each: every file of a directory is read before any
		// error surfaces, so a shared directory reports the first file sorted.
		dir := t.TempDir()
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := testSets([]string{dir}); err == nil {
			t.Errorf("%s was read without an error", name)
			continue
		} else if !strings.Contains(err.Error(), name) {
			t.Errorf("error for %s does not name the file: %v", name, err)
			continue
		}
	}
}

func TestRunTestSetsCountsMissedExpectations(t *testing.T) {
	u := newUniverse()
	for _, sy := range []en16931.Syntax{en16931.SyntaxCII, en16931.SyntaxUBL} {
		if err := u.loadSyntax(sy); err != nil {
			t.Fatal(err)
		}
	}
	st, err := runTestSets(fakeRef(t), u)
	if err != nil {
		t.Fatal(err)
	}
	if st.cases != 1 || st.expect != 2 || st.missed != 1 {
		t.Errorf("stats = %+v, want one payload whose one expectation did not fire", st)
	}
	if len(st.missList) != 1 || !strings.Contains(st.missList[0], "BR-DOES-NOT-FIRE") {
		t.Errorf("miss list = %v", st.missList)
	}
	if got := u.covered["UBL"]["BR-CL-04"]; got.Kind != "official-testset" {
		t.Errorf("BR-CL-04 proven by %+v, want the official test set", got)
	}
}

func TestBasesOf(t *testing.T) {
	got, err := basesOf("ignored", "a.xml, b.xml", 0)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(got, "|") != "a.xml| b.xml" {
		t.Errorf("named bases = %v", got)
	}
	if _, err := basesOf(t.TempDir(), "", 0); err == nil {
		t.Error("an empty reference checkout should be an error, not an empty run")
	}
}

func TestCleanBasesKeepsOnlyDocumentsWithoutFindings(t *testing.T) {
	root := t.TempDir()
	tf := filepath.Join(root, "test", "testfiles")
	ci := filepath.Join(root, "cii", "examples")
	ubl := copySample(t, "ubl-min.xml", tf)
	cii := copySample(t, "cii-xrechnung.xml", ci)
	// A document with a finding is a useless base: every finding on its
	// mutants would then be attributed to the mutation wrongly.
	broken := filepath.Join(tf, "broken.xml")
	// A four letter currency code is the sort of typo that survives XML parsing
	// and produces a finding, which is exactly what makes a document useless as
	// a base: its findings are not caused by the mutation.
	bad := strings.Replace(string(mustRead(t, filepath.Join(sampleDir, "ubl-min.xml"))),
		"<cbc:DocumentCurrencyCode>SEK</cbc:DocumentCurrencyCode>",
		"<cbc:DocumentCurrencyCode>SEKK</cbc:DocumentCurrencyCode>", 1)
	if _, err := en16931.Validate([]byte(bad), en16931.Options{}); err != nil {
		t.Fatalf("the deliberately flawed base does not even parse: %v", err)
	}
	if len(firedIDs(mustValidate(t, []byte(bad)))) == 0 {
		t.Fatal("the deliberately flawed base produces no findings, so the test proves nothing")
	}
	if err := os.WriteFile(broken, []byte(bad), 0o644); err != nil {
		t.Fatal(err)
	}

	got := cleanBases(root, 0)
	for _, f := range got {
		if f == broken {
			t.Errorf("a document with findings was taken as a clean base")
		}
	}
	if len(got) < 2 || !slices.Contains(got, ubl) || !slices.Contains(got, cii) {
		t.Errorf("clean bases = %v, want both samples", got)
	}
	if lim := cleanBases(root, 1); len(lim) < 1 || len(lim) > 2 {
		t.Errorf("limit 1 per syntax returned %d bases: %v", len(lim), lim)
	}
	if none := cleanBases(filepath.Join(root, "nowhere"), 0); len(none) != 0 {
		t.Errorf("a missing checkout gave %v", none)
	}
}

// mustValidate fails the test rather than papering over a validation error.
func mustValidate(t *testing.T, b []byte) *en16931.Report {
	t.Helper()
	rep, err := en16931.Validate(b, en16931.Options{})
	if err != nil {
		t.Fatal(err)
	}
	return rep
}

func TestTargetedBase(t *testing.T) {
	cii := filepath.Join(sampleDir, "cii-xrechnung.xml")
	t.Chdir(t.TempDir())
	b, err := targetedBase([]string{cii}, en16931.SyntaxCII)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), "CrossIndustryInvoice") {
		t.Errorf("base is not CII: %.120s", b)
	}
	if _, err := targetedBase([]string{filepath.Join(sampleDir, "ubl-min.xml")}, en16931.SyntaxCII); err == nil {
		t.Error("no CII document among the bases should be an error")
	}
	if _, err := targetedBase([]string{filepath.Join(t.TempDir(), "gone.xml")}, en16931.SyntaxCII); err == nil {
		t.Error("an unreadable base should be an error")
	}
}

func TestTargetedBasePrefersTheNamedSample(t *testing.T) {
	dir := t.TempDir()
	sample := filepath.Join(dir, "testdata", "samples")
	if err := os.MkdirAll(sample, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sample, "ubl-peppol.xml"), []byte("<Invoice/>"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Chdir(dir)
	b, err := targetedBase(nil, en16931.SyntaxUBL)
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != "<Invoice/>" {
		t.Errorf("got %q, want the sample the command names", b)
	}
}
