package main

import (
	"bytes"
	"compress/gzip"
	"errors"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/dcnws/invoicekit.go/xsd"
)

// The two documents below are a miniature of the shape the real schema sets
// have: a root document that imports another namespace and reaches into it by
// ref, plus declarations that nothing points at. A generator that keeps the
// reachable set and drops the rest is what makes a 3 MB schema fit in 29 KB.
const rootSchema = `<?xml version="1.0" encoding="UTF-8"?>
<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema"
           xmlns:r="urn:test:root"
           xmlns:c="urn:test:child"
           targetNamespace="urn:test:root"
           elementFormDefault="qualified">
  <xs:import namespace="urn:test:child" schemaLocation="child.xsd"/>
  <xs:element name="Order" type="r:OrderType"/>
  <xs:element name="Discarded" type="xs:string"/>
  <xs:complexType name="OrderType">
    <xs:sequence>
      <xs:element name="Reference" type="xs:string"/>
      <xs:element ref="c:Line" minOccurs="0" maxOccurs="unbounded"/>
    </xs:sequence>
  </xs:complexType>
  <xs:complexType name="UnusedType">
    <xs:sequence><xs:element name="Nothing" type="xs:string"/></xs:sequence>
  </xs:complexType>
</xs:schema>
`

const childSchema = `<?xml version="1.0" encoding="UTF-8"?>
<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema"
           xmlns:c="urn:test:child"
           targetNamespace="urn:test:child"
           elementFormDefault="qualified">
  <xs:element name="Line" type="c:LineType"/>
  <xs:element name="OrphanLine" type="c:LineType"/>
  <xs:complexType name="LineType">
    <xs:sequence><xs:element name="Quantity" type="xs:string"/></xs:sequence>
  </xs:complexType>
</xs:schema>
`

// writeSet lays out a two-document schema set and returns its directory.
func writeSet(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "root.xsd"), []byte(rootSchema), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "child.xsd"), []byte(childSchema), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

// TestGenerateKeepsOnlyWhatIsReachable is the generator's whole purpose: the
// output has to hold the pruned subtree, across the import, and nothing else.
func TestGenerateKeepsOnlyWhatIsReachable(t *testing.T) {
	dir := writeSet(t)
	out := filepath.Join(t.TempDir(), "model.xml.gz")

	var stdout, stderr strings.Builder
	args := []string{"-dir", dir, "-schema", "root.xsd",
		"-root", "{urn:test:root}Order", "-out", out}
	if err := run(args, &stdout, &stderr); err != nil {
		t.Fatalf("run: %v (stderr %s)", err, stderr.String())
	}
	if !strings.Contains(stdout.String(), "bytes written") {
		t.Errorf("stdout = %q, want the summary line", stdout.String())
	}

	raw, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(out, dir) && len(raw) < 20 {
		t.Fatalf("output is %d bytes, too small to be a schema", len(raw))
	}
	src := gunzip(t, raw)

	m, err := xsd.Load("model.xml", src, nil)
	if err != nil {
		t.Fatalf("the generated model does not read back: %v\n%s", err, src)
	}
	if !slices.Contains(m.Roots, "{urn:test:root}Order") {
		t.Errorf("roots = %v, want the requested root", m.Roots)
	}
	// A global element that a content model points at by ref has to stay a
	// global declaration in the output, or the ref would not resolve; so the
	// imported Line is a root of the written model as well as a kept decl.
	if !slices.Contains(m.Roots, "{urn:test:child}Line") {
		t.Errorf("roots = %v, want the ref'd element too", m.Roots)
	}
	for _, want := range []string{"{urn:test:root}Order", "{urn:test:child}Line"} {
		if _, ok := m.Decls[want]; !ok {
			t.Errorf("decls = %v, want %s kept", declIDs(m), want)
		}
	}
	for _, unwanted := range []string{"{urn:test:root}Discarded", "{urn:test:child}OrphanLine"} {
		if _, ok := m.Decls[unwanted]; ok {
			t.Errorf("%s survived the prune", unwanted)
		}
	}
	line := m.Decls["{urn:test:child}Line"]
	if line == nil || line.Type == nil || line.Type.Term == nil {
		t.Fatalf("the cross-namespace declaration lost its content model: %+v", line)
	}
	if got := termNames(line.Type.Term); len(got) != 1 || got[0] != "{urn:test:child}Quantity" {
		t.Errorf("content = %v, want the one child element", got)
	}
}

// TestGenerateWithoutGzip writes the schema out as it reads back: the .gz
// suffix is the only thing that decides whether the bytes are deflated.
func TestGenerateWithoutGzip(t *testing.T) {
	dir := writeSet(t)
	out := filepath.Join(t.TempDir(), "model.xml")
	var stdout, stderr strings.Builder
	args := []string{"-dir", dir, "-schema", "root.xsd",
		"-root", "{urn:test:root}Order", "-out", out}
	if err := run(args, &stdout, &stderr); err != nil {
		t.Fatalf("run: %v (stderr %s)", err, stderr.String())
	}
	raw, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "<xs:schema") {
		t.Errorf("output does not look like a schema: %.200s", raw)
	}
	m, err := xsd.Load("model.xml", raw, nil)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if _, ok := m.Decls["{urn:test:root}Order"]; !ok {
		t.Errorf("decls = %v", declIDs(m))
	}
}

// TestBadUsageExitsTwo covers the three flags the command cannot do without,
// and a flag it has never heard of.
func TestBadUsage(t *testing.T) {
	dir := writeSet(t)
	cases := [][]string{
		nil,
		{"-dir", dir, "-schema", "root.xsd"}, // no root
		{"-dir", dir, "-root", "{urn:test:root}Order", "-out", "x.xml"},       // no schema
		{"-dir", dir, "-schema", "root.xsd", "-root", "{urn:test:root}Order"}, // no out
		{"-nope"},
	}
	for i, args := range cases {
		var stdout, stderr strings.Builder
		err := run(args, &stdout, &stderr)
		if err == nil {
			t.Errorf("case %d (%v): no error", i, args)
			continue
		}
		if !errors.Is(err, errUsage) {
			t.Errorf("case %d (%v): %v, want a usage error", i, args, err)
		}
		if stdout.String() != "" {
			t.Errorf("case %d wrote to stdout: %q", i, stdout.String())
		}
	}
}

// TestSchemaErrorsAreNamed so a failure while regenerating says which stage
// broke: an unreadable schema set, or a root that is not in it.
func TestSchemaErrorsAreNamed(t *testing.T) {
	dir := writeSet(t)
	out := filepath.Join(t.TempDir(), "model.xml")

	var stderr strings.Builder
	err := run([]string{"-dir", dir, "-schema", "missing.xsd",
		"-root", "{urn:test:root}Order", "-out", out}, os.Stdout, &stderr)
	if err == nil || !strings.Contains(err.Error(), "load:") {
		t.Errorf("missing schema: %v, want a load error", err)
	}

	stderr.Reset()
	err = run([]string{"-dir", dir, "-schema", "root.xsd",
		"-root", "{urn:test:root}NotThere", "-out", out}, os.Stdout, &stderr)
	if err == nil || !strings.Contains(err.Error(), "prune:") {
		t.Errorf("unknown root: %v, want a prune error", err)
	}

	if _, statErr := os.Stat(out); statErr == nil {
		t.Error("a failed run wrote an output file")
	}

	// An unwritable destination is a write error, not a silent success.
	err = run([]string{"-dir", dir, "-schema", "root.xsd",
		"-root", "{urn:test:root}Order", "-out", filepath.Join(t.TempDir(), "nope", "m.xml")},
		os.Stdout, os.Stderr)
	if err == nil || !strings.Contains(err.Error(), "write:") {
		t.Errorf("unwritable out: %v, want a write error", err)
	}
}

// TestCheckedInModelsAreReproducible regenerates the two embedded validity
// models from the official schema documents and compares the bytes with what
// is checked in. NOTICE.md promises that both are reproducible; this is the
// test that keeps that promise. It skips when the source schemas are not on
// disk, since they are not vendored.
func TestCheckedInModelsAreReproducible(t *testing.T) {
	cases := []struct {
		schemaDir string
		// need lists every source document the run reads, directly or through
		// an import: a set with one file missing is not a broken generator, it
		// is a schema set that was never unpacked here.
		need    []string
		schemas []string
		roots   []string
		want    string
	}{
		{
			schemaDir: "../../../ref-en16931/cii/schema/D16B SCRDM (Subset)/uncoupled clm/CII/uncefact/data/standard",
			need: []string{
				"CrossIndustryInvoice_100pD16B.xsd",
				"CrossIndustryInvoice_ReusableAggregateBusinessInformationEntity_100pD16B.xsd",
			},
			schemas: []string{"CrossIndustryInvoice_100pD16B.xsd"},
			roots:   []string{"{urn:un:unece:uncefact:data:standard:CrossIndustryInvoice:100}CrossIndustryInvoice"},
			want:    "../../internal/en16931/schemas/cii-xsd.xml.gz",
		},
		{
			schemaDir: "../../../schemas-external/ubl/maindoc",
			need: []string{
				"../maindoc/UBL-Invoice-2.1.xsd",
				"../maindoc/UBL-CreditNote-2.1.xsd",
				"../common/UBL-CommonAggregateComponents-2.1.xsd",
				"../common/CCTS_CCT_SchemaModule-2.1.xsd",
				"../common/UBL-CommonSignatureComponents-2.1.xsd",
			},
			schemas: []string{"UBL-Invoice-2.1.xsd", "UBL-CreditNote-2.1.xsd"},
			roots: []string{
				"{urn:oasis:names:specification:ubl:schema:xsd:Invoice-2}Invoice",
				"{urn:oasis:names:specification:ubl:schema:xsd:CreditNote-2}CreditNote",
			},
			want: "../../internal/en16931/schemas/ubl-xsd.xml.gz",
		},
	}
	for _, c := range cases {
		for _, n := range c.need {
			if _, err := os.Stat(filepath.Join(c.schemaDir, n)); err != nil {
				t.Skipf("source schema %s is not unpacked here", n)
			}
		}
		out := filepath.Join(t.TempDir(), filepath.Base(c.want))
		args := []string{"-dir", c.schemaDir}
		for _, sc := range c.schemas {
			args = append(args, "-schema", sc)
		}
		for _, r := range c.roots {
			args = append(args, "-root", r)
		}
		args = append(args, "-out", out)

		var stdout, stderr strings.Builder
		if err := run(args, &stdout, &stderr); err != nil {
			t.Fatalf("regenerate %s: %v (stderr %s)", c.want, err, stderr.String())
		}
		got, err := os.ReadFile(out)
		if err != nil {
			t.Fatal(err)
		}
		want, err := os.ReadFile(c.want)
		if err != nil {
			t.Fatal(err)
		}
		// The only legitimate difference is the directory the provenance
		// comment names: the checked-in bytes came from a checkout where the
		// sources sat in schemas-src/. Everything else has to match exactly.
		gotXML := provenance.ReplaceAllString(string(gunzip(t, got)), `schema files in HERE`)
		wantXML := provenance.ReplaceAllString(string(gunzip(t, want)), `schema files in HERE`)
		if gotXML != wantXML {
			t.Errorf("%s is not reproducible: %d bytes generated vs %d checked in", c.want, len(got), len(want))
			for i := 0; i < len(gotXML) && i < len(wantXML); i++ {
				if gotXML[i] != wantXML[i] {
					t.Errorf("first difference at byte %d:\n generated: %.200s\n checked in: %.200s", i, gotXML[i:], wantXML[i:])
					break
				}
			}
		}
	}
}

// provenance matches the "Derived from" path inside a generated model.
var provenance = regexp.MustCompile(`schema files in [^,]*`)

func gunzip(t *testing.T, data []byte) []byte {
	t.Helper()
	r, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("output is not valid gzip: %v", err)
	}
	defer r.Close()
	out, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("read gzip: %v", err)
	}
	return out
}

// termNames collects the element names a content model accepts, so a test can
// assert on a subtree without spelling out the sequence around it.
func termNames(t *xsd.Term) []string {
	var out []string
	if t.Name != "" {
		out = append(out, t.Name)
	}
	for _, p := range t.Parts {
		out = append(out, termNames(p)...)
	}
	return out
}

func declIDs(m *xsd.Model) []string {
	var out []string
	for id := range m.Decls {
		out = append(out, id)
	}
	return out
}
