package xp

import (
	"math/big"
	"testing"
)

// TestASTStrings checks the debug rendering of every node kind. The compiler
// and the Schematron pipeline print these in diagnostics, so a node that
// renders as "" or panics turns a confusing error into an useless one.
func TestASTStrings(t *testing.T) {
	cases := []struct {
		e    expr
		want string
	}{
		{&litStr{s: "a"}, `"a"`},
		{&litNum{r: big.NewRat(7, 2)}, "3.500000"},
		{&varExpr{name: "x"}, "$x"},
		{&seqExpr{}, "seq(...)"},
		{&ctxItem{}, "."},
		{&binExpr{op: "+", l: &litNum{r: big.NewRat(1, 1)}, r: &litNum{r: big.NewRat(2, 1)}}, "(1.000000 + 2.000000)"},
		{&unExpr{op: "-", x: &litNum{r: big.NewRat(3, 1)}}, "-3.000000"},
		{&ifExpr{}, "if(...)then...else..."},
		{&quantExpr{name: "i"}, "quant $i"},
		{&forExpr{}, "for ... return ..."},
		{&rangeExpr{}, "(to)"},
		{&castExpr{name: "string"}, "(cast as xs:string)"},
		{&castableExpr{name: "integer"}, "(castable as xs:integer)"},
		{&nodeTest{axis: "child", kind: "text"}, "child::text()"},
		{&nodeTest{axis: "child", wild: true}, "child::*"},
		{&nodeTest{axis: "attribute", name: "id", ns: "urn:ram"}, "attribute::id"},
		{&filterExpr{}, "filter(...)"},
		{&funcCall{name: "count"}, "count(...)"},
		{&pathExpr{}, "path(...)"},
	}
	for _, c := range cases {
		if got := c.e.String(); got != c.want {
			t.Errorf("%T.String() = %q, want %q", c.e, got, c.want)
		}
	}
}

// TestCompiledExprString checks that a compiled expression keeps the source it
// was built from, which is what a Schematron report quotes back.
func TestCompiledExprString(t *testing.T) {
	src := `count(//ram:A) ge 2`
	e, err := Compile(src, tns)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	if e.String() != src {
		t.Errorf("Expr.String() = %q, want %q", e.String(), src)
	}
	// A zero Expr has no source, and the evaluator says so instead of
	// evaluating a nil tree.
	if _, err := eval(Expr{}, ctx{doc: doc(t)}); err == nil {
		t.Error("an Expr with no body evaluated, want an error")
	}
}
