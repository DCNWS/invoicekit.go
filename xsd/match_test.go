package xsd

import "testing"

// TestTermAccept covers the in-place Clark comparison: a namespaced term must
// match namespace and local name separately, and must not match a different
// namespace that shares the local name.
func TestTermAccept(t *testing.T) {
	ns := "urn:uno:sm:cbc"
	cases := []struct {
		name  string
		term  Term
		ns    string
		local string
		want  bool
	}{
		{"namespaced hit", Term{Name: clark(ns, "InvoiceLine")}, ns, "InvoiceLine", true},
		{"wrong namespace", Term{Name: clark(ns, "InvoiceLine")}, "urn:other", "InvoiceLine", false},
		{"wrong local name", Term{Name: clark(ns, "InvoiceLine")}, ns, "Invoice", false},
		{"prefixed local name", Term{Name: clark(ns, "Invoice")}, ns, "InvoiceLine", false},
		{"no namespace hit", Term{Name: "Invoice"}, "", "Invoice", true},
		{"no namespace wrong name", Term{Name: "Invoice"}, "", "CreditNote", false},
		{"term unnamespaced, element namespaced", Term{Name: "Invoice"}, ns, "Invoice", false},
		{"term namespaced, element unnamespaced", Term{Name: clark(ns, "Invoice")}, "", "Invoice", false},
		{"brace without close", Term{Name: "{urn:t"}, "urn:t", "", false},
		{"any term takes everything", Term{Kind: Any}, "", "Whatever", true},
		{"any takes other namespace", Term{Kind: Any}, ns, "Whatever", true},
	}
	for _, c := range cases {
		if got := c.term.accept(c.ns, c.local); got != c.want {
			t.Errorf("%s: accept(%q, %q) = %v, want %v", c.name, c.ns, c.local, got, c.want)
		}
	}
}

// TestClosureMatchesMapOrder checks the bitset walk against the general map
// walk, including visit order: the matcher takes the first accepting state in
// the set, so the order is observable in the messages the fixtures pin down.
func TestClosureMatchesMapOrder(t *testing.T) {
	leaf := func(n string) *st { return &st{term: &Term{Name: n}} }
	// a -> b, a -> c, with d reachable both ways and a loop back to a: the
	// diamond and the cycle that choice and maxOccurs="unbounded" produce.
	d := leaf("d")
	a := &st{}
	b := leaf("b")
	c := leaf("c")
	a.eps = []*st{b, c}
	b.eps = []*st{d, a}
	c.eps = []*st{d}
	f := &frag{start: a, end: d}
	indexStates(f)

	for _, in := range [][]*st{{a}, {a, d}, {d, a}, {b, c}} {
		got, want := closure(in), closureMap(in)
		if len(got) != len(want) {
			t.Fatalf("closure(%d) size = %d, want %d", len(in), len(got), len(want))
		}
		for i := range want {
			if got[i] != want[i] {
				t.Fatalf("closure order differs at %d: %v vs %v", i, ids(got), ids(want))
			}
		}
	}

	// A machine wider than the bitset must fall back and agree with closureMap.
	wide := &st{}
	for i := range closureStateMax + 7 {
		wide.eps = append(wide.eps, leaf(name(i)))
	}
	f2 := &frag{start: wide, end: wide.eps[0]}
	indexStates(f2)
	got, want := closure([]*st{wide}), closureMap([]*st{wide})
	if len(got) != len(want) {
		t.Fatalf("wide closure size = %d, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("wide closure order differs at %d", i)
		}
	}
	// The wide machine is past the bitset, so every state carries the sentinel
	// that sends a walk through it to the map path.
	for _, s := range append([]*st{wide}, wide.eps...) {
		if s.idx != closureStateMax {
			t.Fatalf("over-wide machine kept state index %d", s.idx)
		}
	}

	// A machine inside the bitset is numbered from its start state.
	plain := &st{}
	plain.eps = []*st{leaf("x"), leaf("y")}
	indexStates(&frag{start: plain, end: plain.eps[0]})
	if plain.idx != 0 || plain.eps[0].idx != 1 || plain.eps[1].idx != 2 {
		t.Fatalf("indexStates numbered the machine wrongly: %d %d %d", plain.idx, plain.eps[0].idx, plain.eps[1].idx)
	}
}

func name(i int) string {
	return string(rune('a'+i%26)) + string(rune('a'+(i/26)%26)) + string(rune('a'+i/676))
}

func ids(set []*st) []string {
	var out []string
	for _, s := range set {
		if s.term == nil {
			out = append(out, "-")
			continue
		}
		out = append(out, s.term.Name)
	}
	return out
}
