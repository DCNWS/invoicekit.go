package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dcnws/invoicekit.go/internal/en16931"
)

// captureStreams points the package's output seams at buffers for one test, so
// a report can be read instead of printed. Tests in this package never run in
// parallel, which is what makes swapping package variables safe.
func captureStreams(t *testing.T) (out, errOut *bytes.Buffer) {
	t.Helper()
	out, errOut = &bytes.Buffer{}, &bytes.Buffer{}
	prevOut, prevErr := stdout, stderr
	stdout, stderr = out, errOut
	t.Cleanup(func() { stdout, stderr = prevOut, prevErr })
	return out, errOut
}

// stageUniverse builds a small universe by hand. A report test wants a known
// number of asserts, not the ~1,800 the real rule sets carry.
func stageUniverse() *universe {
	u := newUniverse()
	for sy, ids := range map[string][]string{"CII": {"BR-01", "BR-02"}, "UBL": {"BR-01"}} {
		u.asserts[sy] = map[string]string{}
		u.pattern[sy] = map[string]string{}
		u.phases[sy] = map[string][]string{}
		u.patCount[sy] = map[string]int{}
		u.covered[sy] = map[string]source{}
		for _, id := range ids {
			u.asserts[sy][id] = "FLAG"
			u.pattern[sy][id] = "spec"
			u.phases[sy][id] = []string{"identification"}
		}
		u.patCount[sy]["spec"] = len(ids)
		u.phaseIDs[sy] = []string{"identification"}
	}
	u.mark(en16931.SyntaxCII, []string{"BR-01"}, source{Kind: "mutant", What: "BR-01--x.xml"})
	return u
}

func TestCoverageCountsProvenAndMissing(t *testing.T) {
	u := stageUniverse()
	cov := u.coverage()

	if got := cov.totals["CII"]; got != 2 {
		t.Errorf("CII total = %d, want 2", got)
	}
	if got := strings.Join(cov.missing["CII"], ","); got != "BR-02" {
		t.Errorf("CII missing = %s, want BR-02", got)
	}
	if got := cov.bySrc["CII"]["mutant"]; got != 1 {
		t.Errorf("CII by source mutant = %d, want 1", got)
	}
	if got := cov.hits["CII"]["spec"]; got != 1 {
		t.Errorf("CII hits in pattern spec = %d, want 1", got)
	}
}

func TestPrintRunSaysWhatEachPassProved(t *testing.T) {
	u := stageUniverse()
	cov := u.coverage()
	rs := runStats{
		ts:   testSetStats{cases: 5, expect: 3, missed: 3, missList: []string{"CII BR-02 (a.xml#1)"}},
		cr:   corpusStats{files: 4, expect: 6, missed: 1, evalErrs: 0, missList: []string{"BR-02"}},
		ms:   mutantStats{generated: 10, hits: 4, rejected: 2, why: map[string]int{"x": 1, "y": 1}, example: map[string]string{"x": "b.xml m", "y": "b.xml n"}},
		tg:   map[string]targetStats{"CII": {rules: 7, made: 3, nofire: 4}},
		refs: 2,
	}
	out, _ := captureStreams(t)
	printRun(u, cov, rs)
	got := out.String()

	for _, want := range []string{
		"official test-set payloads: 5, must-fire expectations: 3, not fired: 3",
		"  missing: CII BR-02 (a.xml#1)",
		"mutants: 10 generated, 4 produced findings, 2 rejected",
		"targeted CII: 7 candidate asserts, 3 insertion mutants, 4 still silent",
		"CII: 1/2 active asserts proven (mutant=1)",
		"  never fired: BR-02",
		"CII per pattern:",
		"spec",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("report has no %q\n---\n%s", want, got)
		}
	}
	if strings.Contains(got, "targeted UBL") {
		t.Errorf("report claims a targeted UBL pass that never ran:\n%s", got)
	}
}

func TestPrintRunShowsTruncatedMissList(t *testing.T) {
	u := stageUniverse()
	rs := runStats{ts: testSetStats{cases: 1, expect: 50, missed: 50, missList: []string{"one"}}}
	out, _ := captureStreams(t)
	printRun(u, u.coverage(), rs)
	if !strings.Contains(out.String(), "... 49 more") {
		t.Errorf("a capped miss list should say how many it left out:\n%s", out.String())
	}
}

func TestPrintRejectionsRanksAndCaps(t *testing.T) {
	ms := mutantStats{why: map[string]int{}, example: map[string]string{}}
	for i := range 20 {
		k := strings.Repeat("r", 1) + string(rune('a'+i%26))
		ms.why[k] = i + 1
		ms.example[k] = "base.xml mutant"
	}
	ms.why["most"] = 100
	ms.example["most"] = "base.xml first"

	out, _ := captureStreams(t)
	printRejections(ms)
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if len(lines) != 12 {
		t.Fatalf("printed %d reasons, want the 12 most common:\n%s", len(lines), out.String())
	}
	if !strings.HasPrefix(strings.TrimSpace(lines[0]), "100  most") {
		t.Errorf("the commonest reason should lead: %q", lines[0])
	}
}

func TestNoteRejectionGroupsByReason(t *testing.T) {
	st := mutantStats{why: map[string]int{}, example: map[string]string{}}
	long := strings.Repeat("x", 120)
	for _, o := range []outcome{
		{err: errors.New("bad document"), job: job{base: "z.xml", m: mutant{name: "m1"}}},
		{err: errors.New("bad document"), job: job{base: "a.xml", m: mutant{name: "m2"}}},
		{err: errors.New(long), job: job{base: "b.xml", m: mutant{name: "m3"}}},
	} {
		st.noteRejection(o)
	}
	if st.rejected != 3 {
		t.Errorf("rejected = %d, want 3", st.rejected)
	}
	if st.why["bad document"] != 2 {
		t.Errorf("reasons not grouped: %v", st.why)
	}
	if st.example["bad document"] != "a.xml m2" {
		t.Errorf("example should be the first addressable mutant, got %q", st.example["bad document"])
	}
	for k := range st.why {
		if len(k) > 90 {
			t.Errorf("reason %q was not truncated to 90 bytes", k)
		}
	}
}

func TestWriteJSONSummaryIsMachineReadable(t *testing.T) {
	u := stageUniverse()
	cov := u.coverage()
	rs := runStats{ts: testSetStats{cases: 5, expect: 3, missed: 1}, cr: corpusStats{files: 4}, refs: 2,
		tg: map[string]targetStats{"UBL": {rules: 9, made: 2}}}
	path := filepath.Join(t.TempDir(), "summary.json")
	if err := writeJSONSummary(path, u, cov, rs); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var rows []syntaxSummary
	if err := json.Unmarshal(b, &rows); err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 || rows[0].Syntax != "CII" || rows[1].Syntax != "UBL" {
		t.Fatalf("rows = %+v, want CII then UBL", rows)
	}
	if rows[0].Active != 2 || rows[0].Proven != 1 || rows[0].NeverFired[0] != "BR-02" {
		t.Errorf("CII row = %+v", rows[0])
	}
	if rows[1].TargetRules != 9 || rows[0].TargetRules != 0 {
		t.Errorf("targeted stats landed on the wrong row: %+v", rows)
	}
}

func TestWriteMarkdownNamesTheEngineAndTheGaps(t *testing.T) {
	u := stageUniverse()
	cov := u.coverage()
	rs := runStats{ts: testSetStats{cases: 5, expect: 3, missed: 1}, cr: corpusStats{files: 4, expect: 6, missed: 1, evalErrs: 2},
		ms: mutantStats{generated: 10, hits: 4, rejected: 2}, refs: 2}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "VERSION"), []byte("v9.9.9\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Chdir(dir)
	if got := versionString(); got != "v9.9.9" {
		t.Errorf("versionString = %q, want the VERSION file", got)
	}

	path := filepath.Join(dir, "report.md")
	if err := writeMarkdown(path, u, cov, rs); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	got := string(b)
	for _, want := range []string{
		"Engine: `v9.9.9`",
		"- clean reference documents: 2",
		"- corpus replay: 4 harvested mutants, 6 expected firings, 1 did not fire, 2 eval errors",
		"## CII: 1/2 active asserts proven",
		"never fired (1): BR-02",
		"phases run by default: identification",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("report has no %q\n---\n%s", want, got)
		}
	}
}

func TestVersionStringFallsBackToDev(t *testing.T) {
	t.Chdir(t.TempDir())
	if got := versionString(); got != "dev" {
		t.Errorf("versionString without a VERSION file = %q, want dev", got)
	}
}

func TestPhaseOfAndSrcString(t *testing.T) {
	ph := map[string][]string{"BR-01": {"a", "b"}}
	pat := map[string]string{"BR-01": "spec"}
	if got := phaseOf(ph, pat, "spec"); got != "a,b" {
		t.Errorf("phaseOf = %q, want a,b", got)
	}
	if got := phaseOf(ph, pat, "absent"); got != "-" {
		t.Errorf("phaseOf for an unknown pattern = %q, want -", got)
	}
	if got := srcString(map[string]int{"mutant": 3, "official-testset": 1}); got != "mutant=3, official-testset=1" {
		t.Errorf("srcString = %q", got)
	}
	if got := srcString(map[string]int{}); got != "" {
		t.Errorf("srcString of nothing = %q, want empty", got)
	}
}

func TestEnvOr(t *testing.T) {
	t.Setenv("COV_TEST_SET", "given")
	if got := envOr("COV_TEST_SET", "default"); got != "given" {
		t.Errorf("envOr = %q, want the environment's value", got)
	}
	if got := envOr("COV_TEST_UNSET", "default"); got != "default" {
		t.Errorf("envOr for an unset name = %q, want the default", got)
	}
	t.Setenv("COV_TEST_EMPTY", "")
	if got := envOr("COV_TEST_EMPTY", "default"); got != "default" {
		t.Errorf("envOr treats an empty value as unset, got %q", got)
	}
}

func TestFailReportsAndExitsOnce(t *testing.T) {
	_, errOut := captureStreams(t)
	var code int
	prev := exit
	exit = func(c int) { code = c }
	t.Cleanup(func() { exit = prev })

	fail(errors.New("a mutant could not be rebuilt"))
	if code != 1 {
		t.Errorf("exit status = %d, want 1", code)
	}
	if got := strings.TrimSpace(errOut.String()); got != "cov: a mutant could not be rebuilt" {
		t.Errorf("failure was reported as %q", got)
	}
}
