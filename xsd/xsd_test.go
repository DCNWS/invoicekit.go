package xsd

import (
	"strings"
	"testing"

	"github.com/dcnws/invoicekit.go/internal/xp"
)

// seqSrc is a small schema in the CII dialect: named types, sequence children
// by name, one unbounded element and one optional one.
const seqSrc = `<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema"
    targetNamespace="urn:t" xmlns:t="urn:t" elementFormDefault="qualified">
  <xs:element name="root" type="t:Root"/>
  <xs:complexType name="Root">
    <xs:sequence>
      <xs:element name="a" type="xs:string"/>
      <xs:element name="b" type="xs:string" minOccurs="0"/>
      <xs:element name="c" type="xs:string" maxOccurs="unbounded"/>
    </xs:sequence>
  </xs:complexType>
  <xs:complexType name="Pair">
    <xs:sequence>
      <xs:element name="a" type="xs:string" maxOccurs="2"/>
    </xs:sequence>
  </xs:complexType>
</xs:schema>`

// refSrc is the UBL dialect: children are references to global elements.
const refSrc = `<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema"
    targetNamespace="urn:t" xmlns:t="urn:t" elementFormDefault="qualified">
  <xs:element name="d1" type="xs:string"/>
  <xs:element name="d2" type="xs:string"/>
  <xs:element name="root" type="t:Root"/>
  <xs:complexType name="Root">
    <xs:sequence>
      <xs:element ref="t:d1"/>
      <xs:choice>
        <xs:element ref="t:d2"/>
        <xs:element name="d3" type="xs:string"/>
      </xs:choice>
    </xs:sequence>
  </xs:complexType>
</xs:schema>`

// extSrc uses complexContent extension, which UBL uses for its header types.
const extSrc = `<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema"
    targetNamespace="urn:t" xmlns:t="urn:t" elementFormDefault="qualified">
  <xs:element name="root" type="t:Sub"/>
  <xs:complexType name="Base">
    <xs:sequence>
      <xs:element name="p" type="xs:string"/>
    </xs:sequence>
  </xs:complexType>
  <xs:complexType name="Sub">
    <xs:complexContent>
      <xs:extension base="t:Base">
        <xs:sequence>
          <xs:element name="q" type="xs:string"/>
        </xs:sequence>
      </xs:extension>
    </xs:complexContent>
  </xs:complexType>
</xs:schema>`

// dsSrc reproduces the UBL xmldsig schema quirk: the default namespace is XML
// Schema while the target namespace is something else, so an unprefixed
// type="string" has to fall back to the XML Schema builtin.
const dsSrc = `<schema xmlns="http://www.w3.org/2001/XMLSchema"
    xmlns:ds="http://example.com/ds" targetNamespace="http://example.com/ds"
    elementFormDefault="qualified">
  <element name="root" type="ds:Root"/>
  <complexType name="Root">
    <sequence>
      <element name="keyName" type="string"/>
    </sequence>
  </complexType>
</schema>`

func model(t *testing.T, src string) *Model {
	t.Helper()
	m, err := Load("test.xsd", []byte(src), nil)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	return m
}

// check returns the structural findings for one document.
func check(t *testing.T, m *Model, body string) []string {
	t.Helper()
	d, err := xp.ParseXML(body)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	var out []string
	for _, f := range m.Validate(d) {
		out = append(out, f.Message)
	}
	return out
}

func wantFindings(t *testing.T, got []string, want string) {
	t.Helper()
	for _, g := range got {
		if strings.Contains(g, want) {
			return
		}
	}
	t.Fatalf("no finding contains %q; got %v", want, got)
}

func wantClean(t *testing.T, got []string) {
	t.Helper()
	if len(got) != 0 {
		t.Fatalf("expected no findings, got %v", got)
	}
}

func TestSequenceOrder(t *testing.T) {
	m := model(t, seqSrc)
	wantClean(t, check(t, m, `<root xmlns="urn:t"><a/><c/></root>`))
	wantClean(t, check(t, m, `<root xmlns="urn:t"><a/><b/><c/><c/></root>`))
	// out of order: b before a, c before a
	wantFindings(t, check(t, m, `<root xmlns="urn:t"><b/><a/><c/></root>`),
		"element b is not allowed here")
	wantFindings(t, check(t, m, `<root xmlns="urn:t"><c/><a/><c/></root>`),
		"element c is not allowed here")
}

func TestMissingAndRepeated(t *testing.T) {
	m := model(t, seqSrc)
	// c is mandatory (minOccurs defaults to 1)
	wantFindings(t, check(t, m, `<root xmlns="urn:t"><a/></root>`), "missing required content")
	// maxOccurs="2" on Pair is not reachable from root, so a repeats freely here
	wantClean(t, check(t, m, `<root xmlns="urn:t"><a/><c/></root>`))
}

func TestLeafTakesText(t *testing.T) {
	m := model(t, seqSrc)
	wantFindings(t, check(t, m, `<root xmlns="urn:t"><a><x/></a><c/></root>`),
		"takes a text value")
}

func TestUnknownElement(t *testing.T) {
	m := model(t, seqSrc)
	wantFindings(t, check(t, m, `<root xmlns="urn:t"><zzz/><a/><c/></root>`),
		"element zzz is not allowed here")
}

func TestWrongNamespace(t *testing.T) {
	m := model(t, seqSrc)
	wantFindings(t, check(t, m, `<root xmlns="urn:t"><a xmlns="urn:other"/><c/></root>`),
		"element a is not allowed here; expected a")
}

func TestChoiceAndRefs(t *testing.T) {
	m := model(t, refSrc)
	wantClean(t, check(t, m, `<root xmlns="urn:t"><d1/><d2/></root>`))
	wantClean(t, check(t, m, `<root xmlns="urn:t"><d1/><d3/></root>`))
	// both branches of the choice
	wantFindings(t, check(t, m, `<root xmlns="urn:t"><d1/><d2/><d3/></root>`),
		"element d3 is not allowed here")
	// the order of a sequence is still enforced when children are refs
	wantFindings(t, check(t, m, `<root xmlns="urn:t"><d2/><d1/></root>`),
		"element d2 is not allowed here")
}

func TestComplexContentExtension(t *testing.T) {
	m := model(t, extSrc)
	wantClean(t, check(t, m, `<root xmlns="urn:t"><p/><q/></root>`))
	wantFindings(t, check(t, m, `<root xmlns="urn:t"><q/><p/></root>`),
		"element q is not allowed here")
	wantFindings(t, check(t, m, `<root xmlns="urn:t"><p/></root>`), "missing required content")
}

func TestUnprefixedBuiltinType(t *testing.T) {
	m := model(t, dsSrc)
	wantClean(t, check(t, m, `<root xmlns="http://example.com/ds"><keyName>x</keyName></root>`))
	wantFindings(t, check(t, m, `<root xmlns="http://example.com/ds"><other>x</other></root>`),
		"element other is not allowed here")
}

func TestUndeclaredRoot(t *testing.T) {
	m := model(t, seqSrc)
	got := check(t, m, `<nope xmlns="urn:t"/>`)
	wantFindings(t, got, "root element nope is not declared")
}

// TestRoundTrip is the generator's contract: what Write emits must load back
// through Load with the same verdicts, which is what the embedded models rely
// on. The CII and UBL corpora are checked against the written form, not the
// original schema tree.
func TestRoundTrip(t *testing.T) {
	for _, src := range []string{seqSrc, refSrc, extSrc, dsSrc, anySrc} {
		m := model(t, src)
		var sb strings.Builder
		if err := m.Write(&sb); err != nil {
			t.Fatalf("write: %v", err)
		}
		m2, err := Load("written.xsd", []byte(sb.String()), nil)
		if err != nil {
			t.Fatalf("reload: %v\n%s", err, sb.String())
		}
		docs := map[string]string{
			seqSrc: `<root xmlns="urn:t"><a/><b/><c/><zz/></root>`,
			refSrc: `<root xmlns="urn:t"><d2/><d1/><d1/></root>`,
			extSrc: `<root xmlns="urn:t"><q/><p/></root>`,
			dsSrc:  `<root xmlns="http://example.com/ds"><other/></root>`,
			anySrc: `<root xmlns="urn:t"><a/></root>`,
		}
		body := docs[src]
		a, b := check(t, m, body), check(t, m2, body)
		if strings.Join(a, "|") != strings.Join(b, "|") {
			t.Errorf("verdicts differ after round trip:\n  original %v\n  written  %v", a, b)
		}
		if len(a) == 0 {
			t.Errorf("test document produced no findings, so the comparison proves nothing")
		}
	}
}
