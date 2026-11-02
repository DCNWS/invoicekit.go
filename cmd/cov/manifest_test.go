package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/dcnws/invoicekit.go/internal/en16931"
)

// TestReplayKeepsManifestProvenance: the manifest is the record of which pass
// produced each mutant, and a replay knows only the file in front of it. If
// replay overwrote the record, every in-place run of cmd/cov would rewrite the
// committed manifest with a pointer to the file that proves itself, and the
// next diff would look like a change in behaviour when nothing changed. Replaying
// a corpus whose manifest names the mutant must keep that provenance, and must
// write the manifest back unchanged.
func TestReplayKeepsManifestProvenance(t *testing.T) {
	src := filepath.Join("..", "..", "testdata", "mutations", "CII", "BR-17--PayeeTradeParty_empty.xml")
	data, err := os.ReadFile(src)
	if err != nil {
		t.Fatal(err)
	}
	ids := expectedIDs(filepath.Base(src))
	if len(ids) == 0 {
		t.Fatalf("%s names no assertions", src)
	}

	dir := t.TempDir()
	syDir := filepath.Join(dir, string(en16931.SyntaxCII))
	if err := os.MkdirAll(syDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(syDir, filepath.Base(src)), data, 0o644); err != nil {
		t.Fatal(err)
	}
	var ps []proven
	for _, id := range ids {
		ps = append(ps, proven{ID: id, Kind: "mutant", What: "clean-base.xml PayeeTradeParty/empty"})
	}
	want, err := json.MarshalIndent(ps, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(syDir, "manifest.json"), want, 0o644); err != nil {
		t.Fatal(err)
	}

	u := newUniverse()
	if err := u.loadSyntax(en16931.SyntaxCII); err != nil {
		t.Fatal(err)
	}
	if _, err := replayCorpus(dir, u); err != nil {
		t.Fatal(err)
	}
	for _, id := range ids {
		got, ok := u.covered[string(en16931.SyntaxCII)][id]
		if !ok {
			t.Fatalf("%s was not proven by replaying %s", id, src)
		}
		if got.Kind != "mutant" {
			t.Errorf("%s: provenance became %q (%q), want the recorded mutant", id, got.Kind, got.What)
		}
	}
	if err := writeManifests(dir, u); err != nil {
		t.Fatal(err)
	}
	after, err := os.ReadFile(filepath.Join(syDir, "manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(after, want) {
		t.Errorf("a replay rewrote the manifest:\n  before %s\n  after  %s", want, after)
	}
}

// TestManifestSourcesIgnoresReplayEntries: entries a replay wrote itself carry
// no more than the file's own name, so they are not provenance worth keeping.
func TestManifestSourcesIgnoresReplayEntries(t *testing.T) {
	dir := t.TempDir()
	syDir := filepath.Join(dir, string(en16931.SyntaxUBL))
	if err := os.MkdirAll(syDir, 0o755); err != nil {
		t.Fatal(err)
	}
	b, err := json.Marshal([]proven{
		{ID: "BR-01", Kind: "corpus", What: "BR-01+BR-02--Invoice.xml"},
		{ID: "BR-02", Kind: "insertion-mutant", What: "ubl-min.xml Invoice/Party/empty"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(syDir, "manifest.json"), b, 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := manifestSources(dir, en16931.SyntaxUBL)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("read %d sources, want only the insertion mutant: %+v", len(got), got)
	}
	if s := got["BR-02"]; s.Kind != "insertion-mutant" {
		t.Errorf("BR-02 kind = %q, want insertion-mutant", s.Kind)
	}
	if _, err := manifestSources(dir, en16931.SyntaxCII); err != nil {
		t.Errorf("a syntax with no manifest: %v", err)
	}
}
