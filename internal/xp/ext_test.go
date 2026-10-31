package xp

import "testing"

// xdoc carries a spaced IBAN and a truncated one, which is what an XRechnung
// CIUS checkIBAN has to tell apart.
const xdoc = `<rsm:X xmlns:rsm="urn:rsm" xmlns:ram="urn:ram" xmlns:udt="urn:udt">` +
	`<ram:A><udt:V>DE89 3704 0044 0532 0130 00</udt:V></ram:A>` +
	`<ram:B><udt:V>DE89 3704 0044 0532 013</udt:V></ram:B>` +
	`</rsm:X>`

// TestExtFuncVars covers the one shape a real profile's <xsl:function> uses that
// a plain sequence expression does not: a local <xsl:variable> feeding the body.
// XRechnung's CIUS checkIBAN normalises its input through such a variable, so a
// nested Expr in VarBinding.E has to evaluate like any other sub-expression
// rather than tripping the evaluator's unsupported-type branch.
func TestExtFuncVars(t *testing.T) {
	d, err := ParseXML(xdoc)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	ns := map[string]string{"ram": "urn:ram", "udt": "urn:udt", "xtn": "urn:xtn"}
	c := NewCompiler(ns)

	decl, err := c.Declare("xtn:checkIBAN", []string{"iban"})
	if err != nil {
		t.Fatalf("declare: %v", err)
	}
	v, err := c.Compile(`normalize-space(replace($iban, '([\s])', ''))`)
	if err != nil {
		t.Fatalf("compile variable: %v", err)
	}
	decl.Vars = []VarBinding{{Name: "ibanNN", E: v}}
	body, err := c.Compile(`matches($ibanNN, '^[A-Z]{2}[0-9]{20}$')`)
	if err != nil {
		t.Fatalf("compile body: %v", err)
	}
	decl.Body = body

	for _, tc := range []struct {
		src  string
		want bool
	}{
		{`xtn:checkIBAN(//ram:A/udt:V)`, true},
		{`xtn:checkIBAN(//ram:B/udt:V)`, false},
	} {
		call, err := c.Compile(tc.src)
		if err != nil {
			t.Fatalf("compile %s: %v", tc.src, err)
		}
		got, err := call.EvalBool(d, d.Root)
		if err != nil {
			t.Fatalf("eval %s: %v", tc.src, err)
		}
		if got != tc.want {
			t.Errorf("%s = %v, want %v", tc.src, got, tc.want)
		}
	}
}
