package xp

import (
	"strings"
	"testing"
)

// xns is the test document's prefixes plus the schema namespace, which the
// atomic type names need.
var xns = map[string]string{
	"rsm": "urn:rsm", "ram": "urn:ram", "udt": "urn:udt",
	"xs": "http://www.w3.org/2001/XMLSchema",
}

// strCase is one expression and the string value its result must have.
type strCase struct {
	src  string
	want string
}

// runStrCases evaluates each expression against the test document and compares
// the string value of the result.
func runStrCases(t *testing.T, ns map[string]string, cases []strCase) {
	t.Helper()
	d := doc(t)
	for _, c := range cases {
		e, err := Compile(c.src, ns)
		if err != nil {
			t.Fatalf("compile %s: %v", c.src, err)
		}
		v, err := e.Eval(d, d.Root)
		if err != nil {
			t.Fatalf("eval %s: %v", c.src, err)
		}
		if got := stringOf(v); got != c.want {
			t.Errorf("%s = %q, want %q", c.src, got, c.want)
		}
	}
}

// min() and max() walk the whole sequence: one item that is not a number makes
// the answer NaN, and an empty sequence answers empty instead of zero.
func TestMinMax(t *testing.T) {
	runStrCases(t, xns, []strCase{
		{`min((3,1,2))`, "1"},
		{`max((3,1,2))`, "3"},
		{`min(//ram:A/udt:V)`, "0.2"},
		{`max(//udt:V)`, "3"},
		{`min(1)`, "1"},
		{`min((3,'x',2))`, "NaN"},
		{`max((3,'x',2))`, "NaN"},
		{`max((xs:double('NaN'),1))`, "NaN"},
	})
	runBoolCases(t, xns, []boolCase{
		{`count(min(())) = 0`, true},
		{`count(max(//nope)) = 0`, true},
	})
}

// round() rounds half toward positive infinity and round-half-even() to the
// nearest even integer; both leave the empty sequence empty.
func TestRounding(t *testing.T) {
	runStrCases(t, xns, []strCase{
		{`round(2.5)`, "3"},
		{`round(-2.5)`, "-2"},
		{`round(2.49)`, "2"},
		{`round(-0.5)`, "0"},
		{`round('x')`, "NaN"},
		{`round-half-even(2.5)`, "2"},
		{`round-half-even(3.5)`, "4"},
		{`round-half-even(-2.5)`, "-2"},
		{`round(//ram:A[2]/udt:V[1])`, "0"},
	})
	runBoolCases(t, xns, []boolCase{{`count(round(())) = 0`, true}})
}

// fn:substring counts characters, 1-based, after rounding both arguments, and
// clamps them to the string: substring("12345",0,3) is "12".
func TestSubstring(t *testing.T) {
	runStrCases(t, tns, []strCase{
		{`substring('12345',2,3)`, "234"},
		{`substring('12345',0,3)`, "12"},
		{`substring('12345',2)`, "2345"},
		{`substring('12345',1.6,3)`, "234"},
		{`substring('héllo',2,2)`, "él"},
		{`substring('abc',-1,3)`, "a"},
		{`substring('abc',2,10)`, "bc"},
		{`substring('abc',0,0)`, ""},
		{`substring('abc',4)`, ""},
		{`substring(//ram:B/udt:V,1,1)`, "2"},
	})
}

// String and Name are how a caller outside this package reads an evaluation
// result: its string value, and the prefixed name of its first item. A node
// with no name of its own answers with its string value.
func TestStringAndNameHelpers(t *testing.T) {
	d := doc(t)
	cases := []struct {
		src, str, name string
	}{
		{`//ram:B/@id`, "7", "id"},
		{`//ram:A[1]/udt:V`, "1.10", "udt:V"},
		{`//ram:A[1]/udt:V/text()`, "1.10", "1.10"},
		{`//nope`, "", ""},
	}
	for _, c := range cases {
		v, err := mustCompile(t, c.src).Eval(d, d.Root)
		if err != nil {
			t.Fatalf("eval %s: %v", c.src, err)
		}
		if got := String(v); got != c.str {
			t.Errorf("String(%s) = %q, want %q", c.src, got, c.str)
		}
		if got := Name(v); got != c.name {
			t.Errorf("Name(%s) = %q, want %q", c.src, got, c.name)
		}
	}
	if String(nil) != "" || Name(nil) != "" {
		t.Errorf("String/Name of nothing: %q, %q, want empty", String(nil), Name(nil))
	}
}

// An atomic type written as a function call is the lenient form of a cast: a
// value that does not fit becomes NaN or stays a string, so a rule about an
// absent or malformed node answers false instead of ending the validation.
// "cast as" is the strict form, covered in cast_test.go.
func TestAtomicTypeConstructors(t *testing.T) {
	runStrCases(t, xns, []strCase{
		{`xs:integer('3')`, "3"},
		{`xs:decimal(' 1.5 ')`, "1.5"},
		{`xs:double('1e3')`, "1000"},
		{`xs:token(' a ')`, "a"},
		{`xs:normalizedString(' a ')`, "a"},
		{`xs:boolean('true')`, "true"},
		{`xs:boolean('1')`, "true"},
		{`xs:integer('x')`, "NaN"},
		{`xs:nonsense('x')`, "x"},
	})
}

// A function name the engine knows but has not implemented is a different
// diagnosis from one nobody declared, so the rule author is told which of the
// two to fix.
func TestUnsupportedAndUnknownFunctions(t *testing.T) {
	d := doc(t)
	for _, src := range []string{
		`format-number(1,'0')`, `root()`, `current()`, `generate-id()`,
		`compare('a','b')`, `contains-token('a b','b')`, `resolve-uri('a')`,
		`doc('x')`, `document('x')`, `doc-available('x')`, `unparsed-text('x')`,
		`namespace-uri-for-prefix('ram',())`,
	} {
		e, err := Compile(src, xns)
		if err != nil {
			t.Fatalf("compile %s: %v", src, err)
		}
		if _, err := e.Eval(d, d.Root); err == nil || !strings.Contains(err.Error(), "not supported") {
			t.Errorf("%s: got %v, want a %q error", src, err, "not supported")
		}
	}
	for _, src := range []string{`deep-text(//ram:A)`, `no-such-function()`} {
		e, err := Compile(src, xns)
		if err != nil {
			t.Fatalf("compile %s: %v", src, err)
		}
		if _, err := e.Eval(d, d.Root); err == nil || !strings.Contains(err.Error(), "unknown function") {
			t.Errorf("%s: got %v, want an %q error", src, err, "unknown function")
		}
	}
}
