package xp

import "testing"

// dedupDoc is a small tree whose document-order positions are known, so the
// tests below can build node sequences by hand.
func dedupDoc(t *testing.T) (*Doc, map[string]*Node) {
	t.Helper()
	doc, err := ParseXML(`<r xmlns:a="urn:a"><A id="1"><V>1</V><V>2</V></A><B/><A id="2"/></r>`)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	want := map[string]*Node{"r": nil, "A1": nil, "V1": nil, "V2": nil, "B": nil, "A2": nil}
	for _, n := range doc.All {
		switch n.Name {
		case "r":
			want["r"] = n
		case "A":
			if n.Attrs[0].Value == "1" {
				want["A1"] = n
			} else {
				want["A2"] = n
			}
		case "V":
			if n.Text == "1" {
				want["V1"] = n
			} else {
				want["V2"] = n
			}
		case "B":
			want["B"] = n
		}
	}
	for k, n := range want {
		if n == nil {
			t.Fatalf("node %s not found in the test document", k)
		}
	}
	return doc, want
}

func TestDedupSortOrdersAndDedups(t *testing.T) {
	_, n := dedupDoc(t)
	got := dedupSort([]any{n["A2"], n["B"], n["A1"], n["V2"], n["V1"], n["A1"], n["V1"]})
	want := []any{n["A1"], n["V1"], n["V2"], n["B"], n["A2"]}
	if len(got) != len(want) {
		t.Fatalf("got %d nodes, want %d: %v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("node %d: got %v, want %v", i, got[i], want[i])
		}
	}
}

func TestDedupSortLeavesOrderedInputAlone(t *testing.T) {
	_, n := dedupDoc(t)
	in := []any{n["A1"], n["V1"], n["V2"], n["B"], n["A2"]}
	got := dedupSort(in)
	if &got[0] != &in[0] {
		t.Fatalf("an already unique, document-ordered sequence was copied: %p, want %p", &got[0], &in[0])
	}
}

// A text() or @attr step builds a fresh virtual node every time it runs, so
// two of them can stand for the same node in the tree. The tree holds one text
// node per element, so two text nodes that share a parent are that node and
// deduplication collapses them, even though their sort position alone says
// nothing either way. Text of two different elements stays distinct.
func TestDedupSortCollapsesTheSameVirtualNodes(t *testing.T) {
	_, n := dedupDoc(t)
	a := &textNode{parent: n["A1"], data: "12"}
	b := &textNode{parent: n["A1"], data: "12"}
	if x1, x2 := ordOf(a); x1 != n["A1"].Ord || x2 != 1 {
		t.Fatalf("text node sort position: got (%d,%d), want (%d,1)", x1, x2, n["A1"].Ord)
	}
	if got := dedupSort([]any{b, a}); len(got) != 1 {
		t.Fatalf("got %d text nodes, want 1: %v", len(got), got)
	}
	v := &textNode{parent: n["V1"], data: "1"}
	got := dedupSort([]any{a, v, a})
	if len(got) != 2 || got[0] != any(a) || got[1] != any(v) {
		t.Fatalf("text nodes of different elements lost or reordered: %v", got)
	}
	c := &attrNode{parent: n["A1"], idx: 0, a: n["A1"].Attrs[0]}
	d := &attrNode{parent: n["A1"], idx: 0, a: n["A1"].Attrs[0]}
	if got := dedupSort([]any{c, d}); len(got) != 1 {
		t.Fatalf("got %d attribute nodes, want 1: %v", len(got), got)
	}
}

// The same identity has to hold through evaluation, or a union of two steps
// that reach the same attribute counts it twice and "is" of a step with itself
// is false.
func TestVirtualNodeIdentityThroughEvaluation(t *testing.T) {
	d := doc(t)
	cases := []struct {
		src  string
		want bool
	}{
		{`count(//ram:B/@id | //ram:B/@id) = 1`, true},
		{`//ram:B/@id is //ram:B/@id`, true},
		{`//ram:B/@* is //ram:B/@id`, true},
		{`count(//ram:B/@* | //ram:B/@*) = 1`, true},
		{`count(//udt:V/text() | //udt:V/text()) = 4`, true},
		{`//ram:A[1]/udt:V[1]/text() is //ram:A[1]/udt:V[1]/text()`, true},
		{`//ram:A[1]/udt:V[1]/text() is //ram:A[2]/udt:V[1]/text()`, false},
		{`//ram:B/@id is //ram:B/@name`, false},
	}
	for _, c := range cases {
		e, err := Compile(c.src, tns)
		if err != nil {
			t.Fatalf("compile %s: %v", c.src, err)
		}
		got, err := e.EvalBool(d, d.Root)
		if err != nil {
			t.Fatalf("eval %s: %v", c.src, err)
		}
		if got != c.want {
			t.Errorf("%s: got %v, want %v", c.src, got, c.want)
		}
	}
}

// Attribute nodes of the same element are ordered by their position in the
// element, and equal positions are not treated as duplicates.
func TestDedupSortOrdersAttributesByPosition(t *testing.T) {
	doc, err := ParseXML(`<r id="9" name="x"><A id="1" name="y"/></r>`)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	el := doc.All[1]
	got := dedupSort([]any{&attrNode{parent: el, idx: 1, a: el.Attrs[1]}, &attrNode{parent: el, idx: 0, a: el.Attrs[0]}})
	if g, ok := got[0].(*attrNode); !ok || g.idx != 0 {
		t.Fatalf("attributes not in document order: %v", got)
	}
}

func TestDedupSortKeepsDuplicateAtomics(t *testing.T) {
	got := dedupSort([]any{"a", "a", int64(1), int64(1), 2.5})
	if len(got) != 5 {
		t.Fatalf("atomic values were dropped: %v", got)
	}
}
