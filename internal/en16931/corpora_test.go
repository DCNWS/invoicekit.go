package en16931

import (
	"os"
	"path/filepath"
	"testing"
)

// corpusDir locates an upstream checkout used only by the suites that need it.
// The corpora are other parties' repositories, so they are not vendored here:
// point the given environment variable at one, or keep the checkout beside this
// repository (../ref-en16931 when run from the repository root). Candidates are
// tried at every level above the test's working directory, which is this
// package's directory rather than the repository root. An empty result means
// the corpus is absent, and the calling test skips.
func corpusDir(t *testing.T, env, name string) string {
	t.Helper()
	if d := os.Getenv(env); d != "" {
		if _, err := os.Stat(d); err == nil {
			return d
		}
	}
	dir := "."
	for range 4 {
		dir = filepath.Join(dir, "..")
		cand := filepath.Join(dir, name)
		if _, err := os.Stat(cand); err == nil {
			return cand
		}
	}
	return ""
}
