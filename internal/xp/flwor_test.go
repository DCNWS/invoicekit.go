package xp

import (
	"math/big"
	"testing"
)

// TestVars covers the only way a Schematron <let> reaches the evaluator: the
// caller supplies named values and $name reads them. Profile artefacts (XRechnung
// CIUS) declare their regexes and codelists that way, so an unbound variable must
// stay an error rather than becoming an empty string that quietly passes a rule.
func TestVars(t *testing.T) {
	d := doc(t)
	vars := map[string]any{
		"FACTOR":   rat(2),
		"PATTERN":  "^\\s*(\\d+)(\\.\\d+)?\\s*$",
		"CODELIST": " 7 42 ",
	}
	for _, c := range []struct {
		src  string
		want bool
	}{
		{`sum(//ram:A/udt:V) * $FACTOR = 8.6`, true},
		{`matches(//ram:A[1]/udt:V, $PATTERN)`, true},
		{`contains($CODELIST, concat(' ', normalize-space(//ram:B/@id), ' '))`, true},
		{`contains($CODELIST, ' 9 ')`, false},
	} {
		e, err := Compile(c.src, tns)
		if err != nil {
			t.Fatalf("compile %s: %v", c.src, err)
		}
		got, err := e.EvalBoolVars(d, d.Root, vars)
		if err != nil {
			t.Fatalf("eval %s: %v", c.src, err)
		}
		if got != c.want {
			t.Errorf("%s = %v, want %v", c.src, got, c.want)
		}
	}
	// A variable the artefact never declares must fail loudly: a silent empty
	// string would turn every regex rule into a no-op.
	e, err := Compile("$UNSET = 1", tns)
	if err != nil {
		t.Fatalf("compile $UNSET: %v", err)
	}
	if _, err := e.EvalBoolVars(d, d.Root, vars); err == nil {
		t.Error("an unbound variable must be an error, not an empty string")
	}
}

// TestFor covers the FLWOR for/return expression, which quantified predicates do
// not cover: it produces a sequence, so string-join(for ...) can build the IBAN
// rearrangement XRechnung's checkIBAN needs.
func TestFor(t *testing.T) {
	d := doc(t)
	for _, c := range []struct {
		src  string
		want bool
	}{
		{`count(for $x in (1,2,3) return $x * 2) = 3`, true},
		{`sum(for $x in (1,2,3) return $x * 2) = 12`, true},
		{`sum(for $v in //ram:A/udt:V return xs:decimal($v)) = 4.3`, true},
		{`count(for $x in () return $x) = 0`, true},
		{`for $x in (1) return $x = 1`, true},
		{`string-join(for $x in ('a','b') return upper-case($x), '-') = 'A-B'`, true},
		{`count(for $x in (1,2) for $y in (3,4) return $x + $y) = 4`, true},
		{`sum(for $x in (1,2) for $y in (3,4) return $x + $y) = 20`, true},
		{`string-to-codepoints('AB') = (65, 66)`, true},
		{`codepoints-to-string((65, 66)) = 'AB'`, true},
		{`string-join(for $cp in string-to-codepoints('AB') return string($cp - 64), '') = '12'`, true},
		{`replace('a1b2c', '\d', 'X') = 'aXbXc'`, true},
		{`replace('hello world', '(\w+) (\w+)', '$2 $1') = 'world hello'`, true},
		{`replace(//ram:A[1]/udt:V, '[^\d]', '') = '110'`, true},
		{`count(replace((), '\d', 'X')) = 0`, true},
	} {
		e, err := Compile(c.src, tns)
		if err != nil {
			t.Fatalf("compile %s: %v", c.src, err)
		}
		got, err := e.EvalBool(d, d.Root)
		if err != nil {
			t.Fatalf("eval %s: %v", c.src, err)
		}
		if got != c.want {
			t.Errorf("%s = %v, want %v", c.src, got, c.want)
		}
	}
}

// TestCodepointNumbers pins the numeric type of codepoints, which the IBAN
// checksum arithmetic then compares with mod.
func TestCodepointNumbers(t *testing.T) {
	e, err := Compile("string-to-codepoints('Ï')", tns)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	v, err := e.Eval(doc(t), doc(t).Root)
	if err != nil {
		t.Fatalf("eval: %v", err)
	}
	s, ok := v.([]any)
	if !ok || len(s) != 1 {
		t.Fatalf("want a one-item sequence, got %#v", v)
	}
	r, ok := s[0].(*big.Rat)
	if !ok || r.Cmp(rat(207)) != 0 {
		t.Errorf("codepoint = %#v, want 207", s[0])
	}
}
