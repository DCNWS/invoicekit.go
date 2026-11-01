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

// Two text nodes for the same element are different nodes: deduplication works
// on identity, not on document-order position, which is coarse for the virtual
// text and attribute nodes.
func TestDedupSortKeepsDistinctTextNodes(t *testing.T) {
	_, n := dedupDoc(t)
	a := &textNode{parent: n["A1"], data: "12"}
	b := &textNode{parent: n["A1"], data: "12"}
	got := dedupSort([]any{a, b})
	if len(got) != 2 {
		t.Fatalf("got %d text nodes, want 2", len(got))
	}
	if got[0] != any(a) || got[1] != any(b) {
		t.Fatalf("text nodes reordered or lost: %v", got)
	}
	x1, x2 := ordOf(a)
	y1, y2 := ordOf(b)
	if x1 != y1 || x2 != y2 {
		t.Fatalf("the two text nodes should share a document-order position, so identity is the only test")
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
