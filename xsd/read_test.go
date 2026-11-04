package xsd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestLoadRefuses checks the reader's refusals. Each of these is a schema the
// real world produces; a reader that quietly ignores one of them would validate
// against a model that no longer matches the schema it was handed.
func TestLoadRefuses(t *testing.T) {
	cases := []struct {
		name, src, want string
	}{
		{"not xml", `<?xml version="1.0"?><xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema"`, "bad.xsd"},
		{"wrong root", `<html xmlns="http://www.w3.org/1999/xhtml"/>`, "root element is"},
		{"xs:all", `<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema" targetNamespace="urn:t"
  xmlns:t="urn:t"><xs:element name="r" type="t:R"/><xs:complexType name="R"><xs:all/></xs:complexType></xs:schema>`,
			"xs:all is not supported"},
		{"xs:all in a sequence", `<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema" targetNamespace="urn:t"
  xmlns:t="urn:t"><xs:element name="r" type="t:R"/><xs:complexType name="R"><xs:sequence><xs:all/></xs:sequence></xs:complexType></xs:schema>`,
			"xs:all is not supported"},
		{"top level xs:group", `<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema"><xs:group name="g"/></xs:schema>`,
			"xs:group is not supported"},
		{"xs:group in a sequence", `<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema" targetNamespace="urn:t"
  xmlns:t="urn:t"><xs:element name="r" type="t:R"/><xs:complexType name="R"><xs:sequence><xs:group ref="t:g"/></xs:sequence></xs:complexType></xs:schema>`,
			"xs:group is not supported"},
		{"element without name or ref", `<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema" targetNamespace="urn:t"
  xmlns:t="urn:t"><xs:element name="r" type="t:R"/><xs:complexType name="R"><xs:sequence><xs:element type="xs:string"/></xs:sequence></xs:complexType></xs:schema>`,
			"neither name nor ref"},
		{"bad minOccurs", `<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema"><xs:element name="r"><xs:complexType><xs:sequence minOccurs="x"/></xs:complexType></xs:element></xs:schema>`,
			`minOccurs "x"`},
		{"bad maxOccurs", `<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema"><xs:element name="r"><xs:complexType><xs:sequence><xs:element name="a" maxOccurs="many"/></xs:sequence></xs:complexType></xs:element></xs:schema>`,
			`maxOccurs "many"`},
		{"undeclared prefix", `<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema"><xs:element name="r" type="q:R"/></xs:schema>`,
			`undeclared namespace prefix "q"`},
		{"duplicate element", `<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema" targetNamespace="urn:t"><xs:element name="r" type="xs:string"/><xs:element name="r" type="xs:string"/></xs:schema>`,
			"duplicate global element"},
		{"duplicate type", `<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema" targetNamespace="urn:t"><xs:complexType name="R"/><xs:complexType name="R"/></xs:schema>`,
			"duplicate complexType"},
		{"ref to an undeclared element", `<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema" targetNamespace="urn:t"
  xmlns:t="urn:t"><xs:element name="r" type="t:R"/><xs:complexType name="R"><xs:sequence><xs:element ref="t:gone"/></xs:sequence></xs:complexType></xs:schema>`,
			"is not declared"},
		{"element type undefined", `<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema" targetNamespace="urn:t"><xs:element name="r" type="{urn:t}Nope"/></xs:schema>`,
			"type {urn:t}Nope is not defined"},
		{"named type undefined", `<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema" targetNamespace="urn:t"
  xmlns:t="urn:t"><xs:element name="r" type="t:R"/><xs:complexType name="R"><xs:sequence><xs:element name="a" type="t:Nope"/></xs:sequence></xs:complexType></xs:schema>`,
			"complexType {urn:t}Nope is not defined"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Load("bad.xsd", []byte(tc.src), nil)
			if err == nil {
				t.Fatalf("loaded a schema that should be refused; wanted %q", tc.want)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error %q does not mention %q", err, tc.want)
			}
		})
	}
}

// writeSchema drops a file under dir, creating the intermediate directories.
func writeSchema(t *testing.T, dir, name, body string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

const importHead = `<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema"
    targetNamespace="urn:t" xmlns:t="urn:t" elementFormDefault="qualified">`

// TestImportAndInclude reads a schema split across three documents.
func TestImportAndInclude(t *testing.T) {
	dir := t.TempDir()
	writeSchema(t, dir, "sub/common.xsd", importHead+`
  <xs:element name="d1" type="xs:string"/>
</xs:schema>`)
	writeSchema(t, dir, "flat.xsd", importHead+`
  <xs:complexType name="Pair">
    <xs:sequence><xs:element ref="t:d1"/><xs:element name="d2" type="xs:string"/></xs:sequence>
  </xs:complexType>
</xs:schema>`)
	main := writeSchema(t, dir, "main.xsd", importHead+`
  <xs:import schemaLocation="sub/common.xsd" namespace="urn:t"/>
  <xs:include schemaLocation="flat.xsd"/>
  <xs:element name="root" type="t:Pair"/>
</xs:schema>`)

	m, err := Load(main, mustRead(t, main), FileLoader(filepath.Dir(main)))
	if err != nil {
		t.Fatal(err)
	}
	wantClean(t, check(t, m, `<root xmlns="urn:t"><d1/><d2/></root>`))
	wantFindings(t, check(t, m, `<root xmlns="urn:t"><d2/><d1/></root>`), "element d2 is not allowed here")
	if len(m.Decls) != 2 || len(m.Types) != 1 {
		t.Errorf("model has %d declarations and %d types, want 2 and 1", len(m.Decls), len(m.Types))
	}
}

// TestImportWithoutLoader checks that a schema needing another document is
// refused rather than half read: a missing import is a hole in the model, not a
// detail.
func TestImportWithoutLoader(t *testing.T) {
	src := importHead + `
  <xs:import schemaLocation="other.xsd" namespace="urn:o"/>
  <xs:element name="root" type="xs:string"/>
</xs:schema>`
	_, err := Load("main.xsd", []byte(src), nil)
	if err == nil || !strings.Contains(err.Error(), `no loader for schemaLocation "other.xsd"`) {
		t.Fatalf("want a no-loader error, got %v", err)
	}
}

// TestIncludeMissingFile checks the error names both the directory and the
// reference, which is the only way to tell which document to go and look at.
func TestIncludeMissingFile(t *testing.T) {
	dir := t.TempDir()
	main := writeSchema(t, dir, "main.xsd", importHead+`
  <xs:include schemaLocation="absent.xsd"/>
  <xs:element name="root" type="xs:string"/>
</xs:schema>`)
	_, err := Load(main, mustRead(t, main), FileLoader(dir))
	if err == nil || !strings.Contains(err.Error(), "absent.xsd") {
		t.Fatalf("want an error naming absent.xsd, got %v", err)
	}
}

// TestFileLoaderFallback covers the flattened trees: the vendored UBL tree does
// not keep the relative paths the maindoc schemas write, so a reference that
// misses is retried by base name in the root directory.
func TestFileLoaderFallback(t *testing.T) {
	dir := t.TempDir()
	writeSchema(t, dir, "deep.xsd", importHead+`
  <xs:element name="d1" type="xs:string"/>
</xs:schema>`)
	main := writeSchema(t, dir, "main.xsd", importHead+`
  <xs:include schemaLocation="../now/here/deep.xsd"/>
  <xs:element name="root" type="t:R"/>
  <xs:complexType name="R"><xs:sequence><xs:element ref="t:d1"/></xs:sequence></xs:complexType>
</xs:schema>`)
	m, err := Load(main, mustRead(t, main), FileLoader(dir))
	if err != nil {
		t.Fatalf("fallback load: %v", err)
	}
	wantClean(t, check(t, m, `<root xmlns="urn:t"><d1/></root>`))

	l := FileLoader(dir)
	if _, err := l("", "deep.xsd"); err != nil {
		t.Errorf("loader with an empty directory: %v", err)
	}
	if _, err := l(".", "deep.xsd"); err != nil {
		t.Errorf("loader with a dot directory: %v", err)
	}
	if _, err := l(dir, "nothing.xsd"); err == nil {
		t.Error("loader returned bytes for a file that does not exist")
	}
}

// TestLoadMulti reads two documents into one component set, which is how the
// two root elements of a syntax share one model.
func TestLoadMulti(t *testing.T) {
	dir := t.TempDir()
	a := writeSchema(t, dir, "a.xsd", importHead+`
  <xs:element name="ra" type="t:Ta"/>
  <xs:complexType name="Ta"><xs:sequence><xs:element name="x" type="xs:string"/></xs:sequence></xs:complexType>
</xs:schema>`)
	b := writeSchema(t, dir, "b.xsd", `<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema"
    targetNamespace="urn:u" xmlns:u="urn:u" elementFormDefault="qualified">
  <xs:element name="rb" type="u:Tb"/>
  <xs:complexType name="Tb"><xs:sequence><xs:element name="y" type="xs:string" maxOccurs="unbounded"/></xs:sequence></xs:complexType>
</xs:schema>`)
	m, err := LoadMulti(dir, []string{a, b})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := m.Decls["{urn:t}ra"]; !ok {
		t.Error("{urn:t}ra is missing")
	}
	if _, ok := m.Decls["{urn:u}rb"]; !ok {
		t.Error("{urn:u}rb is missing")
	}
	if got := strings.Join(m.Roots, " "); got != "{urn:t}ra {urn:u}rb" {
		t.Errorf("roots in load order: %q", got)
	}
	if !strings.Contains(m.Source, "2 schema files in "+dir) {
		t.Errorf("source %q does not describe the load", m.Source)
	}
	// A multi-document model is not compiled by LoadMulti, so the matcher has
	// to be built on demand without changing the verdict.
	wantClean(t, check(t, m, `<rb xmlns="urn:u"><y/><y/></rb>`))
	wantFindings(t, check(t, m, `<ra xmlns="urn:t"/>`), "missing required content")

	if _, err := LoadMulti(dir, []string{filepath.Join(dir, "absent.xsd")}); err == nil {
		t.Error("a missing schema file loaded without an error")
	}
}

// TestLoadMultiResolvesNamesAgainstDir is how the generator gets invoked: -dir
// says where a schema set lives, so a bare file name has to be enough, and that
// document's own imports resolve under the same directory instead of wherever
// the command happens to be standing.
func TestLoadMultiResolvesNamesAgainstDir(t *testing.T) {
	root := t.TempDir()
	main := filepath.Join(root, "maindoc")
	writeSchema(t, filepath.Join(root, "common"), "b.xsd", `<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema"
    targetNamespace="urn:u" xmlns:u="urn:u" elementFormDefault="qualified">
  <xs:element name="rb" type="u:Tu"/>
  <xs:complexType name="Tu"><xs:sequence><xs:element name="y" type="xs:string"/></xs:sequence></xs:complexType>
</xs:schema>`)
	writeSchema(t, main, "a.xsd", `<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema"
    targetNamespace="urn:t" xmlns:t="urn:t" xmlns:u="urn:u" elementFormDefault="qualified">
  <xs:import namespace="urn:u" schemaLocation="../common/b.xsd"/>
  <xs:element name="ra" type="t:Ta"/>
  <xs:complexType name="Ta"><xs:sequence><xs:element ref="u:rb"/></xs:sequence></xs:complexType>
</xs:schema>`)

	m, err := LoadMulti(main, []string{"a.xsd"})
	if err != nil {
		t.Fatalf("by bare name under -dir: %v", err)
	}
	for _, id := range []string{"{urn:t}ra", "{urn:u}rb"} {
		if _, ok := m.Decls[id]; !ok {
			t.Errorf("%s missing from a name-resolved load", id)
		}
	}
	// A path that resolves where the caller stands still wins over -dir.
	if _, err := LoadMulti(".", []string{filepath.Join(main, "a.xsd")}); err != nil {
		t.Errorf("explicit path: %v", err)
	}
	// A name in neither place is the caller's mistake, and says so.
	if _, err := LoadMulti(main, []string{"nope.xsd"}); err == nil {
		t.Error("a name that exists nowhere loaded without an error")
	}
}

func TestDir(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"schemas-src/ubl/maindoc/x.xsd", "schemas-src/ubl/maindoc"},
		{"x.xsd", "."},
		{"/abs/x.xsd", "/abs"},
		{"", "."},
	} {
		if got := Dir(tc.in); got != tc.want {
			t.Errorf("Dir(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// TestSchemasWrapper covers the generated model form: several target
// namespaces under one xs:schemas document.
func TestSchemasWrapper(t *testing.T) {
	src := `<xs:schemas xmlns:xs="http://www.w3.org/2001/XMLSchema">
  <xs:schema targetNamespace="urn:t" elementFormDefault="qualified">
    <xs:element name="ra" type="{urn:t}Ta"/>
  </xs:schema>
  <xs:schema targetNamespace="urn:t">
    <xs:complexType name="Ta"><xs:sequence><xs:element name="x" form="unqualified" type="xs:string"/></xs:sequence></xs:complexType>
  </xs:schema>
</xs:schemas>`
	m, err := Load("gen.xsd", []byte(src), nil)
	if err != nil {
		t.Fatal(err)
	}
	// A child declared without qualification sits in no namespace.
	wantClean(t, check(t, m, `<ra xmlns="urn:t"><x xmlns=""/></ra>`))
	wantFindings(t, check(t, m, `<ra xmlns="urn:t"><x xmlns="urn:t"/></ra>`), "element x is not allowed here")
}

// TestUnqualifiedForm checks elementFormDefault and its per-element override.
// The CII schemas are unqualified: their children carry no namespace, so a
// checker that assumed qualification would report every CII document as empty.
func TestUnqualifiedForm(t *testing.T) {
	src := `<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema"
    targetNamespace="urn:c" elementFormDefault="unqualified">
  <xs:element name="root" type="R"/>
  <xs:complexType name="R">
    <xs:sequence>
      <xs:element name="plain" type="xs:string"/>
      <xs:element name="forced" type="xs:string" form="qualified"/>
    </xs:sequence>
  </xs:complexType>
</xs:schema>`
	m, err := Load("cii.xsd", []byte(src), nil)
	if err != nil {
		t.Fatal(err)
	}
	wantClean(t, check(t, m, `<root xmlns="urn:c"><plain xmlns=""/><forced xmlns="urn:c"/></root>`))
	wantFindings(t, check(t, m, `<root xmlns="urn:c"><plain xmlns="urn:c"/></root>`), "element plain is not allowed here")
}

// TestNamedSimpleTypeAndLeaf checks that a named simple type is a leaf: the
// package does not check datatypes, so the reference only has to be known.
func TestNamedSimpleTypeAndLeaf(t *testing.T) {
	src := importHead + `
  <xs:simpleType name="Code"><xs:restriction base="xs:token"/></xs:simpleType>
  <xs:complexType name="Blank"/>
  <xs:element name="root" type="t:R"/>
  <xs:complexType name="R">
    <xs:sequence>
      <xs:element name="a" type="t:Code"/>
      <xs:element name="b" type="t:Blank"/>
      <xs:element name="c"><xs:complexType><xs:simpleContent><xs:extension base="xs:string"/></xs:simpleContent></xs:complexType></xs:element>
    </xs:sequence>
  </xs:complexType>
</xs:schema>`
	m := model(t, src)
	wantClean(t, check(t, m, `<root xmlns="urn:t"><a>x</a><b/><c>y</c></root>`))
	// Every one of them is a leaf, so children are a defect.
	wantFindings(t, check(t, m, `<root xmlns="urn:t"><a><z/></a><b/><c/></root>`), "takes a text value")
}

// TestGlobalRefAndAnonymousType checks two declarations that are not
// components: a global element written as a ref, and an element whose type is
// written inline.
func TestGlobalRefAndAnonymousType(t *testing.T) {
	src := importHead + `
  <xs:element name="d1" type="xs:string"/>
  <xs:element ref="t:d1"/>
  <xs:element name="root">
    <xs:complexType>
      <xs:choice>
        <xs:element ref="t:d1"/>
        <xs:element name="inl"><xs:complexType><xs:sequence><xs:element name="deep" type="xs:string"/></xs:sequence></xs:complexType></xs:element>
      </xs:choice>
    </xs:complexType>
  </xs:element>
  <xs:complexType/>
</xs:schema>`
	m := model(t, src)
	wantClean(t, check(t, m, `<root xmlns="urn:t"><d1>x</d1></root>`))
	wantClean(t, check(t, m, `<root xmlns="urn:t"><inl><deep/></inl></root>`))
	wantFindings(t, check(t, m, `<root xmlns="urn:t"><inl/></root>`), "missing required content")
	// The anonymous type is not addressable, and the global ref declares
	// nothing of its own.
	if _, ok := m.Types["{urn:t}"]; ok {
		t.Error("an anonymous type was registered under the target namespace")
	}
	if len(m.Roots) != 2 {
		t.Errorf("roots = %v, want the two named global elements", m.Roots)
	}
}

// TestExtensionBaseVariants checks complexContent against the bases that appear
// in the two syntaxes: a named base, and the implicit anyType.
func TestExtensionBaseVariants(t *testing.T) {
	src := importHead + `
  <xs:element name="root" type="t:Sub"/>
  <xs:element name="top" type="t:Rest"/>
  <xs:complexType name="Base"><xs:sequence><xs:element name="p" type="xs:string"/></xs:sequence></xs:complexType>
  <xs:complexType name="Sub">
    <xs:complexContent><xs:extension base="t:Base"><xs:choice><xs:element name="q" type="xs:string"/><xs:element name="r" type="xs:string"/></xs:choice></xs:extension></xs:complexContent>
  </xs:complexType>
  <xs:complexType name="Rest">
    <xs:complexContent><xs:restriction base="xs:anyType"><xs:sequence><xs:element name="s" type="xs:string" minOccurs="0"/></xs:sequence></xs:restriction></xs:complexContent>
  </xs:complexType>
</xs:schema>`
	m := model(t, src)
	wantClean(t, check(t, m, `<root xmlns="urn:t"><p/><r/></root>`))
	wantFindings(t, check(t, m, `<root xmlns="urn:t"><r/><p/></root>`), "element r is not allowed here")
	wantClean(t, check(t, m, `<top xmlns="urn:t"/>`))
	wantClean(t, check(t, m, `<top xmlns="urn:t"><s/></top>`))
}

func mustRead(t *testing.T, p string) []byte {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return b
}
