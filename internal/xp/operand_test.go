package xp

import (
	"strings"
	"testing"
)

// A missing operand is a mistake in the expression, not the empty sequence.
// Every expression funnels through one place, so "1 +", an empty predicate, a
// call argument that is not there and an expression that stops before its
// operator all fail the same way instead of quietly compiling.
func TestMissingOperandFailsToCompile(t *testing.T) {
	for _, src := range []string{
		"", "   ", "1 +", "1 and", "1 div", "-", "a or -", "1 to",
		"a[b or]", "foo(1,)", "(1,)", "[]", "for $x in 1 return",
		"if (true()) then 1 else", "$x =",
	} {
		if _, err := Compile(src, nil); err == nil || !strings.Contains(err.Error(), "expected operand") {
			t.Errorf("Compile(%q): %v, want an \"expected operand\" error", src, err)
		}
	}
}

// "()" and a bare "/" are legal XPath with real values: the empty sequence and
// the root node. Hand-written Schematron uses context="/" and context="//A", so
// both have to compile rather than be refused as a step that never arrives.
func TestBareRootPath(t *testing.T) {
	d := doc(t)
	cases := []struct {
		src  string
		want int
	}{
		{"()", 0},
		{"/", 1},
		{"//rsm:X", 1},
		{"//*", 8},
		{"/rsm:X/ram:A", 2},
	}
	for _, c := range cases {
		x, err := Compile("count("+c.src+")", tns)
		if err != nil {
			t.Fatalf("compile count(%s): %v", c.src, err)
		}
		v, err := x.Eval(d, d.RootElem())
		if err != nil {
			t.Fatalf("eval count(%s): %v", c.src, err)
		}
		if n, _ := numOf(v); n == nil || n.Cmp(rat(int64(c.want))) != 0 {
			t.Errorf("count(%s) = %v, want %d", c.src, v, c.want)
		}
	}
	// The bare "/" selects the document node itself, which is what lets a rule
	// context such as "/" see the root element as its child.
	v, err := mustCompile(t, "/").Eval(d, d.RootElem())
	if err != nil {
		t.Fatal(err)
	}
	if seq := seqOf(v); len(seq) != 1 || seq[0] != any(d.Root) {
		t.Errorf("/ = %#v, want the document node %#v", v, d.Root)
	}
}

// A slash that opens a step still has to be followed by one: "a/" is a mistake,
// not the path "a".
func TestStepMustFollowSlash(t *testing.T) {
	for _, src := range []string{"a/", "a//", "/rsm:X/"} {
		if _, err := Compile(src, tns); err == nil {
			t.Errorf("Compile(%q) accepted a path with a missing step", src)
		}
	}
}

func mustCompile(t *testing.T, src string) Expr {
	t.Helper()
	x, err := Compile(src, tns)
	if err != nil {
		t.Fatalf("compile %s: %v", src, err)
	}
	return x
}
