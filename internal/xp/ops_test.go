package xp

import (
	"strings"
	"testing"
)

// TestWordOperators covers the operators the lexer spells as words rather than
// symbols: the short-circuit logical operators, the value comparisons and the
// word-form arithmetic operators. Schematron texts write "and", "or", "eq" and
// "div" constantly, and each of them is a separate branch in evalBin.
func TestWordOperators(t *testing.T) {
	runBoolCases(t, tns, []boolCase{
		{`count(//ram:A) = 2 and count(//ram:B) = 1`, true},
		{`count(//ram:A) = 2 and count(//ram:B) = 2`, false},
		{`count(//ram:A) = 2 or count(//ram:B) = 2`, true},
		{`count(//ram:A) = 3 or count(//ram:B) = 2`, false},
		// "and" and "or" are short-circuit: the branch that is not taken is
		// never evaluated, so a call that would fail there stays harmless.
		{`false() and nosuchfunction() = 1`, false},
		{`true() or nosuchfunction() = 1`, true},
		// Value comparisons take single items; the engine is lenient and uses
		// the first item of either side.
		{`count(//ram:A) eq 2`, true},
		{`count(//ram:A) ne 3`, true},
		{`count(//ram:A) lt 3`, true},
		{`count(//ram:A) le 2`, true},
		{`count(//ram:A) gt 1`, true},
		{`count(//ram:A) ge 2`, true},
		{`'a' eq 'a' and 'a' ne 'b' and 'b' gt 'a' and 'a' le 'a'`, true},
		{`//ram:B/@id eq 7`, true},
		{`//ram:A eq //ram:A`, true},
		// Word-form arithmetic.
		{`sum(//ram:A/udt:V) div 2 eq 2.15`, true},
		{`count(//ram:A) idiv 2 eq 1`, true},
		{`count(//ram:A) mod 2 eq 0`, true},
		{`7 mod 2 eq 1 and -7 mod 2 eq -1`, true},
		{`1 + 2 * 3 eq 7 and (1 + 2) * 3 eq 9`, true},
		{`5 - 2 - 1 eq 2`, true},
		{`1.5 div 0.5 eq 3 and 1.5 mod 1 eq 0.5`, true},
	})
}

// TestUnaryOperators covers unary minus and plus. An empty sequence stays
// empty instead of becoming zero, and text that is not a number becomes NaN,
// which every rule that reads a missing or malformed amount depends on.
func TestUnaryOperators(t *testing.T) {
	runStrCases(t, tns, []strCase{
		{`-count(//ram:A)`, "-2"},
		{`+count(//ram:A)`, "2"},
		{`-'3'`, "-3"},
		{`-'x'`, "NaN"},
		{`- -1`, "1"},
		{`1 - -1`, "2"},
		{`count(-())`, "0"},
		{`count(-//ram:nope)`, "0"},
		{`count(-//ram:A)`, "1"},
		{`-(1,2)`, "-1"}, // a sequence is coerced through its first item
	})
}

// TestArithErrors pins the two arithmetic failures: division by zero raises an
// error, while a non-numeric operand answers NaN.
func TestArithErrors(t *testing.T) {
	d := doc(t)
	for _, src := range []string{`1 div 0`, `1 idiv 0`, `1 mod 0`, `count(//ram:A) div (1 - 1)`} {
		if _, err := mustCompile(t, src).Eval(d, d.Root); err == nil || !strings.Contains(err.Error(), "division by zero") {
			t.Errorf("%s: got %v, want a %q error", src, err, "division by zero")
		}
	}
	runStrCases(t, tns, []strCase{
		{`'abc' + 1`, "NaN"},
		{`1 + 'abc'`, "NaN"},
		{`//ram:nope + 1`, ""}, // an empty operand gives an empty sequence
		{`//ram:A + ()`, ""},
		{`count(//ram:nope + 1)`, "0"},
	})
}
