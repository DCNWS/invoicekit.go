package main

import (
	"cmp"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/dcnws/invoicekit.go/internal/en16931"
)

// TestHarvestOrderIndependent: the sweep validates tens of thousands of
// mutants in parallel, and thousands of them prove the same asserts. Which of
// them enters the corpus may not depend on the order the workers happened to
// finish in, or every run rewrites a different set of test files and a replay
// test can never lock the corpus in. The same work fed forwards and backwards,
// through one worker so completion order is input order, must harvest the same
// corpus.
func TestHarvestOrderIndependent(t *testing.T) {
	base := filepath.Join("..", "..", "testdata", "samples", "ubl-min.xml")
	work, err := mutantJobs([]string{base}, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(work) < 50 {
		t.Fatalf("only %d mutants from %s", len(work), base)
	}
	backward := slices.Clone(work)
	slices.Reverse(backward)

	fwd := harvestTo(t, t.TempDir(), work)
	rev := harvestTo(t, t.TempDir(), backward)
	if !slices.Equal(fwd, rev) {
		t.Errorf("harvest depends on completion order: %d mutants forwards, %d backwards", len(fwd), len(rev))
		for i := range min(len(fwd), len(rev)) {
			if fwd[i] != rev[i] {
				t.Errorf("first difference at %d:\n  forwards  %s\n  backwards %s", i,
					strings.ReplaceAll(fwd[i], "\n", " "), strings.ReplaceAll(rev[i], "\n", " "))
				break
			}
		}
	}
	if len(fwd) == 0 {
		t.Fatal("nothing was harvested")
	}
}

// harvestTo runs one sweep into dir and returns its harvested mutants as
// name plus content, sorted by name.
func harvestTo(t *testing.T, dir string, work []job) []string {
	t.Helper()
	u := newUniverse()
	if err := u.loadSyntax(en16931.SyntaxUBL); err != nil {
		t.Fatal(err)
	}
	if st := harvest(work, 1, dir, u); st.hits == 0 {
		t.Fatal("no mutant fired")
	}
	var out []string
	for _, sy := range []en16931.Syntax{en16931.SyntaxCII, en16931.SyntaxUBL} {
		files, err := filepath.Glob(filepath.Join(dir, string(sy), "*.xml"))
		if err != nil {
			t.Fatal(err)
		}
		slices.SortFunc(files, func(a, b string) int { return cmp.Compare(filepath.Base(a), filepath.Base(b)) })
		for _, f := range files {
			b, err := os.ReadFile(f)
			if err != nil {
				t.Fatal(err)
			}
			out = append(out, filepath.Base(f)+string(b))
		}
	}
	return out
}
