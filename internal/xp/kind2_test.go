package xp

import (
	"fmt"
	"strings"
	"testing"
)

// TestKindTestArguments covers the arguments a kind test accepts. element()
// and attribute() take a name, which constrains the match exactly as a bare
// name test does; the sequence-type argument that may follow it is skipped,
// because this tree carries no schema types. node(), text() and comment()
// have no arguments in the profile's expressions and any that appear are
// skipped, and processing-instruction's target is kept but never matches,
// since the parser drops comments and instructions.
func TestKindTestArguments(t *testing.T) {
	runBoolCases(t, tns, []boolCase{
		{`count(//element()) = 8`, true},
		{`count(//element(*)) = 8`, true},
		{`count(//element(ram:*)) = 3`, true},
		// The name inside element() narrows the match; it is not a wildcard.
		{`count(//element(ram:A)) = 2`, true},
		{`count(//element(B)) = 0`, true},
		{`count(//element(ram:A, xs:integer)) = 2`, true},
		{`count(//attribute()) = 1`, true},
		{`count(//attribute(*)) = 1`, true},
		{`count(//attribute(id)) = 1`, true},
		{`count(//attribute(ram:*)) = 0`, true},
		{`count(//attribute(nope)) = 0`, true},
		{`count(//ram:B/attribute(id)) = 1`, true},
		{`count(//text()) = 4`, true},
		{`count(//text('x')) = 4`, true},
		{`count(//node()) = 8`, true},
		{`count(//node(1)) = 8`, true},
		{`count(//comment()) = 0`, true},
		{`count(//processing-instruction()) = 0`, true},
		{`count(//processing-instruction('x')) = 0`, true},
		// A kind test never matches the other kinds.
		{`count(//ram:B/text()) = 0`, true},
		{`count(//udt:V/attribute()) = 0`, true},
		{`count(//ram:B/@id/attribute()) = 0`, true},
		{`count(//ram:B/@id/parent::ram:B) = 1`, true},
		{`count(//ram:B/@id/ancestor::ram:B) = 1`, true},
		{`count(//ram:B/@id/ancestor-or-self::ram:B) = 1`, true},
		{`count(//ram:B/@id/..) = 1`, true},
		{`count(//udt:V/text()/..) = 4`, true},
		{`count(//udt:V/text()/parent::udt:V) = 4`, true},
		{`count(//ram:A/udt:V/text()/..) = 3`, true},
		{`count(/..) = 0`, true},
		{`count(//self::node()) = 9`, true},
	})
}

// TestKindTestParseErrors checks that a malformed kind test is a compile
// failure rather than a step that quietly selects everything.
func TestKindTestParseErrors(t *testing.T) {
	for _, c := range []struct{ src, want string }{
		{`//element(`, "expected node name"},
		{`//element(unk:*)`, `undefined prefix "unk"`},
		{`//attribute(unk:x)`, `undefined prefix "unk"`},
		{`//text(`, "unclosed"},
		{`descendant-or-self::`, "expected"},
	} {
		if _, err := Compile(c.src, tns); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: got %v, want an error containing %q", c.src, err, c.want)
		}
	}
}

// TestDedupLargeNodeSet exercises the hashed path of dedupSort, which the
// linear scan below dedupSmall does not reach: a big union that repeats every
// node must still come back once, in document order.
func TestDedupLargeNodeSet(t *testing.T) {
	const n = 60
	var b strings.Builder
	b.WriteString(`<r>`)
	for i := 1; i <= n; i++ {
		fmt.Fprintf(&b, `<i k="%d"><v>%d</v></i>`, i, i)
	}
	b.WriteString(`</r>`)
	d, err := ParseXML(b.String())
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if got := len(d.All); got != 2*n+2 {
		t.Fatalf("parsed %d nodes, want %d", got, 2*n+2)
	}
	runBoolOn(t, d, nil, []boolCase{
		{`count(//i) = 60`, true},
		{`count(//i | //i) = 60`, true},
		{`count(//i | //i | //i) = 60`, true},
		{`count(//i | //v) = 120`, true},
		{`count(//v | //i) = 120`, true},
		{`count(//i/@k | //i/@k) = 60`, true},
		{`count(//i/text()) = 0`, true},
		{`count(//v/text() | //v/text()) = 60`, true},
		{`count(for $i in (1 to 200) return //i[$i]) = 60`, true},
		{`//i[60]/@k = '60'`, true},
		{`count(//i[last()]/following::i) = 0`, true},
		{`count(//i[1]/following-sibling::i) = 59`, true},
		{`count(//i[30]/preceding::v) = 29`, true},
	})
}
