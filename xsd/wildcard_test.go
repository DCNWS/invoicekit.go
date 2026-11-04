package xsd

import (
	"strings"
	"testing"
)

// anySrc puts an xs:any wildcard after a named element. The wildcard carries
// no occurrence attributes, so it has to keep the XSD defaults of one.
const anySrc = `<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema"
    targetNamespace="urn:t" xmlns:t="urn:t" elementFormDefault="qualified">
  <xs:element name="root" type="t:Root"/>
  <xs:complexType name="Root">
    <xs:sequence>
      <xs:element name="a" type="xs:string"/>
      <xs:any namespace="##other"/>
    </xs:sequence>
  </xs:complexType>
</xs:schema>`

// anyWild swaps the wildcard's occurrence attributes in anySrc.
func anyWild(t *testing.T, attrs string) string {
	t.Helper()
	out := strings.Replace(anySrc, `<xs:any namespace="##other"/>`,
		`<xs:any namespace="##other" `+attrs+`/>`, 1)
	if out == anySrc {
		t.Fatal("anySrc has no wildcard to patch")
	}
	return out
}

// TestAnyWildcard pins the wildcard's occurrence counts. UBL's
// ExtensionContentType is a sequence holding one xs:any, so a wildcard that
// silently reads as zero occurrences rejects the content of every real
// extension: the position accepts nothing and demands nothing.
func TestAnyWildcard(t *testing.T) {
	m := model(t, anySrc)
	wantClean(t, check(t, m, `<root xmlns="urn:t"><a/><ext xmlns="urn:other"/></root>`))
	// The checker does not police the wildcard's namespace constraint, so a
	// child in the schema's own namespace passes as well.
	wantClean(t, check(t, m, `<root xmlns="urn:t"><a/><ext/></root>`))
	// One element is required, and its name may be anything.
	wantFindings(t, check(t, m, `<root xmlns="urn:t"><a/></root>`),
		"missing required content: expected any element")
	// A wildcard's own children are not described by the schema, so they
	// are not checked at all.
	wantClean(t, check(t, m, `<root xmlns="urn:t"><a/><ext><deep xmlns="urn:deep"/></ext></root>`))
	// ...and exactly one is allowed, so the sequence still has an end.
	wantFindings(t, check(t, m, `<root xmlns="urn:t"><a/><x/><y/></root>`),
		"element y is not allowed here")
}

func TestAnyOccurrences(t *testing.T) {
	t.Run("optional", func(t *testing.T) {
		m := model(t, anyWild(t, `minOccurs="0"`))
		wantClean(t, check(t, m, `<root xmlns="urn:t"><a/></root>`))
		wantClean(t, check(t, m, `<root xmlns="urn:t"><a/><x xmlns="urn:o"/></root>`))
	})
	t.Run("unbounded", func(t *testing.T) {
		m := model(t, anyWild(t, `maxOccurs="unbounded"`))
		wantClean(t, check(t, m, `<root xmlns="urn:t"><a/><x/><y/><z/></root>`))
	})
	t.Run("twice", func(t *testing.T) {
		m := model(t, anyWild(t, `maxOccurs="2"`))
		wantClean(t, check(t, m, `<root xmlns="urn:t"><a/><x/><y/></root>`))
		wantFindings(t, check(t, m, `<root xmlns="urn:t"><a/><x/><y/><z/></root>`),
			"element z is not allowed here")
	})
	t.Run("bad occurrence", func(t *testing.T) {
		if _, err := Load("bad.xsd", []byte(anyWild(t, `maxOccurs="many"`)), nil); err == nil {
			t.Error("maxOccurs=\"many\" on an xs:any loaded without an error")
		}
	})
}
