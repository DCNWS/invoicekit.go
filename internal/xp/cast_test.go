package xp

import "testing"

// TestCastable covers "castable as", which the Peppol BIS national rules use to
// test an identifier's lexical form without raising an error. Castable never
// fails: it answers false where the corresponding cast would.
func TestCastable(t *testing.T) {
	// The test document's prefixes are rsm/ram/udt, so the schema namespace has
	// to be declared explicitly for the atomic type names.
	ns := map[string]string{"xs": "http://www.w3.org/2001/XMLSchema"}
	runBoolCases(t, ns, []boolCase{
		{`'12' castable as xs:integer`, true},
		{`'12.5' castable as xs:integer`, false},
		{`'' castable as xs:integer`, false},
		{`'x' castable as xs:decimal`, false},
		{`'12.50' castable as xs:decimal`, true},
		{`'2026-04-10' castable as xs:date`, true},
		{`'2026-4-10' castable as xs:date`, false},
		{`'abc' castable as xs:date`, false},
		{`'anything' castable as xs:string`, true},
		{`() castable as xs:integer`, true}, // empty sequence casts to the empty sequence
		{`number(3) castable as xs:integer`, true},
	})
}
