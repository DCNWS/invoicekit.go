package xp

import "testing"

// Kind tests appear in the XRechnung profile: an ID element is matched on its
// text content, normalize-space(text()) reads a date node, and parent::node()
// reaches the enclosing element. The tree keeps character data on the element
// instead of as separate nodes, so text() yields one synthesised node per
// element that has any.
func TestKindTests(t *testing.T) {
	d := doc(t)
	cases := []struct {
		src  string
		want bool
	}{
		{"count(//text()) = 4", true},
		{"count(//udt:V/text()) = 4", true},
		{"count(//ram:B/text()) = 0", true},
		{"//udt:V/text() = '1.10'", true},
		{"count(//udt:V[text() = '2']) = 1", true},
		{"count(//ram:B/udt:V/text()[. = '2']) = 1", true},
		{"normalize-space(//ram:B/udt:V/text()) = '2'", true},
		{"normalize-space(//ram:A/text()) = ''", true},
		{"count(//ram:A[not(text())]) = 2", true},
		{"string-length(//ram:A[2]/udt:V[1]/text()) = 4", true},
		{"sum(//udt:V/text()) = 6.3", true},
		{"some $v in //udt:V/text() satisfies matches($v, '^\\d$')", true},
		{"count(//ram:A/child::node()) = 3", true},
		{"count(//ram:A/parent::node()) = 1", true},
		{"count(//ram:B/udt:V/parent::node()) = 1", true},
		{"name(//ram:B/udt:V/parent::node()) = 'ram:B'", true},
		{"count(//ram:B/@id/parent::node()) = 1", true},
		{"count(//ram:A/parent::*) = 1", true},
		{"count(//comment()) = 0", true},
		{"count(//processing-instruction()) = 0", true},
		{"count(//ram:B/udt:V | //ram:A/udt:V) = 4", true},
	}
	for _, c := range cases {
		e, err := Compile(c.src, tns)
		if err != nil {
			t.Errorf("compile %s: %v", c.src, err)
			continue
		}
		got, err := e.EvalBool(d, d.Root)
		if err != nil {
			t.Errorf("eval %s: %v", c.src, err)
			continue
		}
		if got != c.want {
			t.Errorf("%s = %v, want %v", c.src, got, c.want)
		}
	}
}
