package xp

import (
	"testing"
)

const tdoc = `<rsm:X xmlns:rsm="urn:rsm" xmlns:ram="urn:ram" xmlns:udt="urn:udt">` +
	`<ram:A><udt:V>1.10</udt:V></ram:A>` +
	`<ram:A><udt:V>0.20</udt:V><udt:V>3</udt:V></ram:A>` +
	`<ram:B id="7"><udt:V>2</udt:V></ram:B>` +
	`</rsm:X>`

var tns = map[string]string{"rsm": "urn:rsm", "ram": "urn:ram", "udt": "urn:udt"}

func doc(t *testing.T) *Doc {
	t.Helper()
	d, err := ParseXML(tdoc)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	return d
}

func TestBool(t *testing.T) {
	d := doc(t)
	cases := []struct {
		src  string
		want bool
	}{
		{"count(//udt:V) = 4", true},
		{"sum(//ram:A/udt:V) = 4.3", true},
		{"sum(//ram:A/udt:V) = 4.30", true},
		{"0.1 + 0.2 = 0.3", true},
		{"sum(//ram:A/udt:V ! xs:decimal(.)) = 4.3", true},
		{"count(//ram:A/udt:V ! xs:decimal(.)) = 3", true},
		{"count(//ram:A[1]/udt:V) = 1", true},
		{"count(//ram:A[2]/udt:V) = 2", true},
		{"count((//ram:A)[2]/udt:V) = 2", true},
		{"count(//ram:A[last()]) = 1", true},
		{"count(//ram:A[position() = 1]) = 1", true},
		{"//@id = 7", true},
		{"count(//@id) = 1", true},
		{"count(//ram:B/@id[. = '7']) = 1", true},
		{"not(//ram:A/udt:V = 0.2)", false},
		{"not(//ram:A/udt:V = 0.25)", true},
		{"every $v in //ram:A/udt:V satisfies xs:decimal($v) > 0", true},
		{"some $v in //ram:B/udt:V satisfies $v = 2", true},
		{"count(//ram:A/udt:V) != count(//ram:A) div 2", true},
		{"upper-case(//ram:B/@id) = '7'", true},
		{"contains(' A B ', concat(' ', normalize-space(//ram:B/@id), ' '))", false},
		{"contains(' 7 A ', concat(' ', normalize-space(//ram:B/@id), ' '))", true},
		{"count(distinct-values(//udt:V)) = 4", true},
		{"count(distinct-values(//ram:A/udt:V)) = 3", true},
		{`matches(//ram:A[1]/udt:V, '^\s*(\d+)(\.\d+)?\s*$')`, true},
		{`count(//ram:B/@id[matches(., '^[0-9]+$')]) = 1`, true},
		{"round(1.5) = 2 and round(-1.5) = -1", true},
		{"floor(-1.5) = -2 and ceiling(-1.5) = -1", true},
		{"abs(-2.5) = 2.5", true},
		{"name(//ram:B) = 'ram:B'", true},
		{"local-name(//ram:B) = 'B'", true},
		{"count(//ram:B/ancestor::rsm:X) = 1", true},
		{"count(//ram:B/parent::rsm:X) = 1", true},
		{"substring-after('abc-def', '-') = 'def'", true},
		{"substring-before('abc-def', '-') = 'abc'", true},
		{"string-length(normalize-space(' a  b ')) = 3", true},
		{"translate('abcabc', 'ab', 'xy') = 'xycxyc'", true},
		{"translate('ABC123', '1234567890', '') = 'ABC'", true},
		{"translate('abc', 'abc', 'de') = 'de'", true},
		{"translate('a', 'abc', 'x') = 'x'", true},
		{"translate('abc', '', 'xy') = 'abc'", true},
		{"translate('\u00e4bc', 'abc', 'xyz') = '\u00e4yz'", true},
		{"string-length(translate(//ram:A[1]/udt:V, '1234567890', '')) = 1", true},
		{"if (count(//ram:A) = 2) then 1 else 2", true},
		{"not(some $x in () satisfies $x = 1)", true},
		{"count(//ram:A/*[self::udt:V]) = 3", true},
		{"//ram:A[2]/udt:V[1] = 0.20", true},
		{"sum(//ram:A/udt:V) = round(sum(//ram:A/udt:V))", false},
		{"string-length(//ram:A[2]/udt:V[1]) = 4", true},
		{"normalize-space(//ram:A[1]/udt:V) = '1.10'", true},
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

// TestDescendantOrSelfAnchoring pins the "//" abbreviation to its XPath
// meaning, /descendant-or-self::node()/: the document node is part of the
// sequence, so an expression such as "//rsm:X" selects the root element
// itself. Schematron rule contexts are anchored that way, and a bare "*" test
// would drop the document node and lose the root element.
func TestDescendantOrSelfAnchoring(t *testing.T) {
	d := doc(t)
	cases := []struct {
		src           string
		wantDocFocus  int
		wantRootFocus int
	}{
		{`//rsm:X`, 1, 1},
		{`//ram:B`, 1, 1},
		{`//ram:A/udt:V`, 3, 3},
		{`/descendant-or-self::node()/rsm:X`, 1, 1},
		{`descendant-or-self::node()/rsm:X`, 1, 0},
	}
	for _, c := range cases {
		x, err := Compile("count("+c.src+")", tns)
		if err != nil {
			t.Fatalf("compile %s: %v", c.src, err)
		}
		for _, f := range []struct {
			name string
			n    *Node
			want int
		}{{"document", d.Root, c.wantDocFocus}, {"root element", d.RootElem(), c.wantRootFocus}} {
			v, err := x.Eval(d, f.n)
			if err != nil {
				t.Fatalf("eval %s at %s: %v", c.src, f.name, err)
			}
			if n, _ := numOf(v); n == nil || n.Cmp(rat(int64(f.want))) != 0 {
				t.Errorf("count(%s) at %s = %v, want %d", c.src, f.name, v, f.want)
			}
		}
	}
}

func TestValues(t *testing.T) {
	d := doc(t)
	for _, c := range []struct{ src, want string }{
		{"sum(//ram:A/udt:V)", "4.3"},
		{"count(//ram:A)", "2"},
		{"//ram:A[1]/udt:V", "1.10"},
		{"concat(//ram:A/udt:V[1], '-', //ram:B/@id)", "1.100.20-7"},
	} {
		e, err := Compile(c.src, tns)
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

// TestFocusDefaulting covers the zero-argument forms of name(), local-name()
// and string-length(), which default to the focus node. The EN 16931 UBL
// schema depends on them: //*[ends-with(name(), 'Amount')]/@currencyID.
func TestFocusDefaulting(t *testing.T) {
	d := doc(t)
	for _, c := range []struct {
		src  string
		want bool
	}{
		{`count(//ram:A/udt:V[string-length() = 4]) = 2`, true},
		{`count(//udt:V[name() = 'udt:V']) = 4`, true},
		{`count(//*[ends-with(local-name(), 'V')]) = 4`, true},
		{`string-length(//ram:A[1]/udt:V) = 4`, true},
		{`name(//ram:B) = 'ram:B'`, true},
		{`local-name(//ram:B) = 'B'`, true},
	} {
		e, err := Compile(c.src, tns)
		if err != nil {
			t.Fatalf("compile %s: %v", c.src, err)
		}
		v, err := e.Eval(d, d.Root)
		if err != nil {
			t.Fatalf("eval %s: %v", c.src, err)
		}
		if got := boolean(v); got != c.want {
			t.Errorf("%s = %v, want %v", c.src, got, c.want)
		}
	}
}

// TestProxFollowing covers the preceding:: and following:: axes, used by
// UBL-SR-47 to detect duplicate elements. Ancestors are excluded from
// preceding:: and descendants from following::.
func TestProxFollowing(t *testing.T) {
	d := doc(t)
	for _, c := range []struct {
		src  string
		want bool
	}{
		{`count(//ram:B/preceding::udt:V) = 3`, true},
		{`count(//ram:B[1]/preceding::ram:A) = 2`, true},
		{`count(//ram:A[1]/following::udt:V) = 3`, true},
		{`count(//ram:A[1]/following::ram:B) = 1`, true},
		{`count(//ram:A[2]/preceding-sibling::ram:A) = 1`, true},
		// A node is never in its own preceding or following axis.
		{`count(//ram:B[1]/preceding::ram:B) = 0`, true},
		{`count(//ram:B[1]/following::ram:B) = 0`, true},
	} {
		e, err := Compile(c.src, tns)
		if err != nil {
			t.Fatalf("compile %s: %v", c.src, err)
		}
		v, err := e.Eval(d, d.Root)
		if err != nil {
			t.Fatalf("eval %s: %v", c.src, err)
		}
		if got := boolean(v); got != c.want {
			t.Errorf("%s = %v, want %v", c.src, got, c.want)
		}
	}
}

// TestNaN documents the handling of non-numeric amounts. Strict XPath raises a
// dynamic error, but an invoice carrying "zz" as an amount must be reported by
// the business rule that compares it, so arithmetic yields NaN and every
// comparison against NaN is false except "!=".
func TestNaN(t *testing.T) {
	d := doc(t)
	for _, c := range []struct {
		src  string
		want bool
	}{
		{`xs:decimal('zz') != 1`, true},
		{`not(xs:decimal('zz') = 1)`, true},
		{`not(xs:decimal('zz') < 1)`, true},
		{`not(xs:decimal('zz') > 1)`, true},
		{`not(1 <= xs:decimal('zz'))`, true},
		{`not(sum(('zz', '1')) = 1)`, true},
		{`sum(('zz', '1')) != 1`, true},
		{`not(round(xs:decimal('zz')) = 0)`, true},
		// A numeric operand on its own still behaves normally.
		{`sum(('1.10', '0.20')) = 1.3`, true},
	} {
		e, err := Compile(c.src, tns)
		if err != nil {
			t.Fatalf("compile %s: %v", c.src, err)
		}
		v, err := e.Eval(d, d.Root)
		if err != nil {
			t.Fatalf("eval %s: %v", c.src, err)
		}
		if got := boolean(v); got != c.want {
			t.Errorf("%s = %v, want %v", c.src, got, c.want)
		}
	}
}

// TestAtomicContextItem pins the "." step: it is the XPath context item, which
// may be any atomic value, not a self::node() test. A predicate over the output
// of fn:tokenize sees strings, so "." must yield the string itself or every
// starts-with/normalize-space test inside the predicate silently fails. The
// XRechnung BR-DE-18 rule filters tokenize($note, '(\r?\n)') that way.
func TestAtomicContextItem(t *testing.T) {
	d := doc(t)
	cases := []struct {
		src  string
		want bool
	}{
		{"count(tokenize('a b c', ' ')[starts-with(., 'a')]) = 1", true},
		{"count(tokenize('a b c', ' ')[. = 'c']) = 1", true},
		{"count(tokenize(' a b ', ' ')[normalize-space(.) = 'a']) = 1", true},
		{"count(tokenize('1 2 3', ' ')[. = '2']) = 1", true},
		{"tokenize('a b c', ' ')[2] = 'b'", true},
		{"count(.[. = 1]) = 0", true},
		{"count(//ram:B/udt:V[. = 2]) = 1", true},
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

// boolCase is one expression and the effective boolean value it must produce.
type boolCase struct {
	src  string
	want bool
}

// runBoolCases compiles and evaluates each expression against the test
// document and requires the expected effective boolean value. The tests for
// individual language features share this harness.
func runBoolCases(t *testing.T, ns map[string]string, cases []boolCase) {
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
		if got := boolean(v); got != c.want {
			t.Errorf("%s = %v, want %v", c.src, got, c.want)
		}
	}
}

// seqCase is one expression and the sequence it must produce, compared item by
// item on the atomic string value.
type seqCase struct {
	src  string
	want any
}

// runSeqCases compiles and evaluates each expression against the test document
// and requires the expected sequence.
func runSeqCases(t *testing.T, cases []seqCase) {
	t.Helper()
	d := doc(t)
	for _, c := range cases {
		e, err := Compile(c.src, tns)
		if err != nil {
			t.Fatalf("compile %s: %v", c.src, err)
		}
		got, err := e.Eval(d, d.Root)
		if err != nil {
			t.Fatalf("eval %s: %v", c.src, err)
		}
		want := seqOf(c.want)
		if len(seqOf(got)) != len(want) {
			t.Errorf("%s = %v, want %v", c.src, got, c.want)
			continue
		}
		for i, it := range seqOf(got) {
			if stringOf(it) != stringOf(want[i]) {
				t.Errorf("%s item %d = %q, want %q", c.src, i, stringOf(it), stringOf(want[i]))
			}
		}
	}
}
