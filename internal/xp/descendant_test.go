package xp

import "testing"

// The "//" steps are evaluated in one pass when they are followed by a child
// step, which changes how the answer is computed but must not change the
// answer. These cases pin the positional semantics that the fused walk has to
// keep: a predicate binds to the children of each parent, not to the whole
// node-set.
func TestDescendantChildPaths(t *testing.T) {
	d := doc(t)
	cases := []struct {
		src  string
		want bool
	}{
		{"count(//udt:V) = 4", true},
		{"count(//*) = 8", true},
		{"count(//*/*) = 7", true},
		{"count(//*/udt:V) = 4", true},
		{"count(//udt:V[1]) = 3", true},
		{"count(//udt:V[last()]) = 3", true},
		{"count(//ram:A/udt:V[2]) = 1", true},
		{"count(//ram:A/*[position() = 1]) = 2", true},
		{"count(//*/*[self::udt:V]) = 4", true},
		{"count(//ram:A[*][1]) = 1", true},
		{"//ram:A[2]/udt:V[1] = '0.20'", true},
		{"count(//ram:B[@id]/udt:V) = 1", true},
		{"count(//*[local-name() = 'V']) = 4", true},
		{"count(//ram:A/udt:V) + count(//udt:V) = 7", true},
	}
	for _, tc := range cases {
		e, err := Compile(tc.src, tns)
		if err != nil {
			t.Fatalf("compile %s: %v", tc.src, err)
		}
		got, err := e.EvalBool(d, d.Root)
		if err != nil {
			t.Fatalf("eval %s: %v", tc.src, err)
		}
		if got != tc.want {
			t.Errorf("%s: got %v, want %v", tc.src, got, tc.want)
		}
	}
}
