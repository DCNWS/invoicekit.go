package xp

import "testing"

// TestRange covers the "to" range operator. The Peppol BIS check-digit helpers
// build their digit lists with it, together with reverse() and positional
// access into the resulting sequence.
func TestRange(t *testing.T) {
	runBoolCases(t, tns, []boolCase{
		{`count((1 to 3)) = 3`, true},
		{`string-join((1 to 3), ',') = '1,2,3'`, true},
		{`string-join((0 to 0), ',') = '0'`, true},
		{`count((3 to 1)) = 0`, true},
		{`(1 to 5)[3] = 3`, true},
		{`sum(for $i in (1 to 4) return $i * 2) = 20`, true},
		{`string-join(reverse((1 to 3)), ',') = '3,2,1'`, true},
		{`string-join(reverse(('a', 'b')), ',') = 'b,a'`, true},
		{`count(reverse(())) = 0`, true},
		// The shape of u:gln: digits of a number, reversed, weighted.
		{`string-join(reverse(for $i in string-to-codepoints('123') return $i - 48), ',') = '3,2,1'`, true},
		{`sum(for $i in (0 to 2) return (1 to 3)[$i + 1] * (1 + (($i + 1) mod 2) * 2)) = 14`, true},
	})
}

// TestRangeBadBounds pins the behaviour of non-integer bounds: a range over
// something that is not a number is an error, not a silent empty sequence.
func TestRangeBadBounds(t *testing.T) {
	d := doc(t)
	for _, src := range []string{`(1 to //ram:A)`, `('a' to 3)`} {
		e, err := Compile(src, tns)
		if err != nil {
			t.Fatalf("compile %s: %v", src, err)
		}
		if _, err := e.Eval(d, d.Root); err == nil {
			t.Errorf("%s evaluated, want an error", src)
		}
	}
}
