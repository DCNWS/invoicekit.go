package xp

import (
	"strings"
	"testing"
)

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

// TestCast covers the strict form, "cast as". The engine is deliberately
// lax about the numeric subtypes: an invoice amount is untyped text, and a
// validator that rejected "1.50" as an xs:integer would report an engine
// failure instead of the business rule. What must fail is text that is not a
// number at all, and a type name nobody declared.
func TestCast(t *testing.T) {
	runStrCases(t, xns, []strCase{
		{`'3' cast as xs:integer`, "3"},
		{`'3' cast as xs:integer + 1`, "4"},
		{`'1.50' cast as xs:decimal`, "1.5"},
		{`' 12 ' cast as xs:decimal`, "12"},
		{`'1e3' cast as xs:double`, "1000"},
		{`3 cast as xs:string`, "3"},
		{`//ram:B/@id cast as xs:string`, "7"},
		{`'2026-04-10' cast as xs:date`, "2026-04-10"},
		{`' a ' cast as xs:token`, " a "}, // a string cast keeps the text as it is
		{`'true' cast as xs:boolean`, "true"},
		{`'1' cast as xs:boolean`, "true"},
		{`true() cast as xs:integer`, "1"},
		{`1.5 cast as xs:integer`, "1.5"}, // lax: the value is kept, not truncated
		{`count(//ram:A cast as xs:integer)`, "2"},
		{`count(('1','2') cast as xs:integer)`, "2"},
		{`count(//ram:nope cast as xs:integer)`, "0"}, // absent nodes stay absent
		{`count(() cast as xs:string)`, "0"},
	})
	d := doc(t)
	for _, c := range []struct{ src, want string }{
		{`'x' cast as xs:integer`, "cannot cast x to xs:integer"},
		{`//ram:A cast as xs:integer > 1`, ""}, // a sequence cast is fine, comparison is not an error
		{`'yes' cast as xs:boolean`, "cannot cast"},
		{`'3' cast as xs:nonsense`, "unsupported cast type xs:nonsense"},
	} {
		x, err := Compile(c.src, xns)
		if err != nil {
			t.Fatalf("compile %s: %v", c.src, err)
		}
		_, err = x.Eval(d, d.Root)
		if c.want == "" {
			if err != nil {
				t.Errorf("%s: unexpected error %v", c.src, err)
			}
			continue
		}
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: got %v, want an error containing %q", c.src, err, c.want)
		}
	}
}
