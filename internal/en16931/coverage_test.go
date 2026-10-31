package en16931

import (
	"path/filepath"
	"strings"
	"testing"
)

// TestMutationCorpus replays the harvested mutation corpus. Every file was
// produced by mutating a document that validates cleanly, and its name encodes
// the assert ids it proved, joined with "+" (e.g.
// "BR-02+BR-03--ExchangedDocument_empty.xml"). The corpus is the regression net
// against false negatives: if a rule stops firing, a corpus file starts failing.
func TestMutationCorpus(t *testing.T) {
	root := filepath.Join("..", "..", "testdata", "mutations")
	var files, checked, fails, evalErrs int
	for _, sy := range []string{"CII", "UBL"} {
		for _, f := range glob(t, filepath.Join(root, sy), "*.xml") {
			base := filepath.Base(f)
			i := strings.Index(base, "--")
			if i <= 0 {
				t.Errorf("%s: file name does not encode the expected rule ids", base)
				continue
			}
			ids := strings.Split(base[:i], "+")
			rep, err := Validate(read(t, f), Options{})
			if err != nil {
				t.Errorf("%s: %v", base, err)
				continue
			}
			fired := map[string]bool{}
			for _, fi := range rep.Findings {
				if fi.Err != "" {
					evalErrs++
					t.Errorf("%s: rule %s could not be evaluated: %s", base, fi.ID, fi.Err)
				}
				fired[fi.ID] = true
			}
			for _, id := range ids {
				checked++
				if !fired[id] {
					fails++
					t.Errorf("%s: expected %s to fire, got %v", base, id, rep.IDs())
				}
			}
			files++
		}
	}
	t.Logf("%d harvested mutants, %d must-fire assertions, %d failures, %d eval errors",
		files, checked, fails, evalErrs)
	if files == 0 {
		t.Fatal("mutation corpus is empty")
	}
}
