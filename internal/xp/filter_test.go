package xp

import (
	"strings"
	"testing"
)

// TestStandaloneFilters covers a filter whose base is not a step: a bare
// predicate on the context item, and a predicate on a parenthesised
// expression. Schematron's assertion and rule contexts are written this way
// once the variable bindings have been substituted into them.
func TestStandaloneFilters(t *testing.T) {
	runBoolCases(t, tns, []boolCase{
		{`count([1]) = 1`, true},
		{`count([2]) = 0`, true},
		{`count([1]/rsm:X/ram:A) = 2`, true},
		{`count([self::node()]) = 1`, true},
		// The context item is the document node, which no name test matches.
		{`count([self::rsm:X]) = 0`, true},
		{`count(//ram:A[.]) = 2`, true},
		{`count((1,2,3)[2]) = 1`, true},
		{`(1,2,3)[2] = 2`, true},
		{`(1,2,3)[last()] = 3`, true},
		{`(1)[2] = 1`, false},
		{`count((1,2,3)[. > 1]) = 2`, true},
		{`count((1,2,3)[position() > 1]) = 2`, true},
		{`count((//ram:A, //ram:A)) = 4`, true}, // a comma sequence keeps duplicates
		{`count((//ram:A | //ram:A)) = 2`, true},
		{`count((//ram:A)[1]/udt:V) = 1`, true},
		{`count((//ram:A)[last()]/udt:V) = 2`, true},
		{`//ram:A[2][1]/udt:V[1] = '0.20'`, true},
		{`count(//ram:A[udt:V][udt:V = 3]) = 1`, true},
		{`count(//ram:B[@id][1]/udt:V) = 1`, true},
	})
}

// TestScopedVariables covers the entry points the Schematron validator calls:
// a pattern parameter arrives as a global, an assertion's let variables as
// vars, and a variable the rule never bound is an error rather than an empty
// string, so a typo in a rule cannot quietly pass.
func TestScopedVariables(t *testing.T) {
	d := doc(t)
	x := mustCompile(t, `//ram:A[udt:V = $want or @id = $id]`)

	g := map[string]any{"want": "0.20", "id": "7"}
	v, err := x.EvalBoolScoped(d, d.Root, g, nil)
	if err != nil || !v {
		t.Errorf("globals: got %v (%v), want true", v, err)
	}
	if n, err := x.EvalScoped(d, d.Root, map[string]any{"want": "99", "id": "9"}, nil); err != nil || boolean(n) {
		t.Errorf("globals, no match: got %v (%v), want false", n, err)
	}

	// A local binding of the same name wins over the global one.
	y := mustCompile(t, `count(//udt:V[. = $v]) = 1`)
	if v, err := y.EvalBoolScoped(d, d.Root, map[string]any{"v": "1.10"}, map[string]any{"v": "3"}); err != nil || !v {
		t.Errorf("shadowed variable: got %v (%v), want true", v, err)
	}
	if v, err := y.EvalBoolScoped(d, d.Root, map[string]any{"v": "1.10"}, nil); err != nil || !v {
		t.Errorf("global only: got %v (%v), want true", v, err)
	}

	for _, src := range []string{`//ram:A[$nope = 1]`, `$nope = 1`} {
		if _, err := mustCompile(t, src).EvalBoolScoped(d, d.Root, nil, nil); err == nil || !strings.Contains(err.Error(), "undefined variable $nope") {
			t.Errorf("%s: got %v, want an %q error", src, err, "undefined variable")
		}
	}

	// The narrower entry points the validator uses when it has no globals.
	if got, err := mustCompile(t, `$v`).EvalVars(d, d.Root, map[string]any{"v": "3"}); err != nil || stringOf(got) != "3" {
		t.Errorf("EvalVars: got %q (%v), want %q", stringOf(got), err, "3")
	}
	if got, err := mustCompile(t, `$v = 3`).EvalBoolVars(d, d.Root, map[string]any{"v": "3"}); err != nil || !got {
		t.Errorf("EvalBoolVars: got %v (%v), want true", got, err)
	}
	if _, err := mustCompile(t, `$v`).EvalVars(d, d.Root, nil); err == nil || !strings.Contains(err.Error(), "undefined variable") {
		t.Errorf("EvalVars with no binding: got %v, want an %q error", err, "undefined variable")
	}
}
