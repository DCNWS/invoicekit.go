package xsd

import (
	"fmt"
	"slices"
	"strings"

	"github.com/dcnws/invoicekit.go/internal/xp"
)

// Finding is one schema validity error.
type Finding struct {
	// Path is the element path from the document root, local names only.
	Path string
	// Message describes the defect and, where useful, what was expected.
	Message string
}

// String renders a finding the way the CLI prints it.
func (f Finding) String() string {
	if f.Path == "" {
		return f.Message
	}
	return f.Path + ": " + f.Message
}

// maxExpand bounds how many times a bounded repeated part is unrolled, keeping
// the state machine small. Real invoice schemas use 1 or unbounded; a bound
// above the cap is matched as unbounded and reported loosely.
const maxExpand = 32

// st is one matcher state. A state with a term consumes one matching element
// and then continues through eps.
type st struct {
	term *Term
	eps  []*st
	// idx is the state's number within its machine, assigned by indexStates.
	// The closure walk uses it as a bit position, which is what makes spotting
	// a state it has already seen cost nothing.
	idx int
}

// frag is a compiled content model.
type frag struct {
	start, end *st
	// first is the epsilon closure of start: the states a document can be in
	// before it has matched any child at all. It is the same for every element
	// of the type, so it is worked out once here rather than on every check.
	first []*st
}

// compile builds a state machine that accepts exactly the sequences a content
// model allows. Repeated parts are compiled afresh so occurrence counts stay
// exact.
func compile(t *Term) *frag {
	s, e := &st{}, &st{}
	wirePart(s, e, t)
	f := &frag{start: s, end: e}
	indexStates(f)
	f.first = closure([]*st{s})
	return f
}

// indexStates numbers the states of one machine from 0. The numbers are only
// used as bit positions inside that machine, so they never have to be unique
// across machines, and every closure walk stays inside one. A machine wider
// than the bitset numbers every state past the end, which sends any walk that
// touches it down the map path: checking one input state then covers the whole
// machine.
func indexStates(f *frag) {
	var all []*st
	seen := map[*st]bool{}
	var walk func(s *st)
	walk = func(s *st) {
		if seen[s] {
			return
		}
		seen[s] = true
		all = append(all, s)
		for _, e := range s.eps {
			walk(e)
		}
	}
	walk(f.start)
	if len(all) > closureStateMax {
		for _, s := range all {
			s.idx = closureStateMax
		}
		return
	}
	for i, s := range all {
		s.idx = i
	}
}

// Compile prepares every content model in the model for matching. A model is
// read by many validations at once — the library caches one per syntax — so
// nothing on the validation path may fill in a lazily compiled part of it:
// Load calls this before the model is handed out.
func (m *Model) Compile() {
	seen := map[*Type]bool{}
	var walkTerm func(p *Term)
	walkType := func(t *Type) {
		if t == nil || seen[t] {
			return
		}
		seen[t] = true
		if t.Term != nil {
			t.fragFor()
		}
		walkTerm(t.Term)
	}
	walkTerm = func(p *Term) {
		if p == nil {
			return
		}
		if p.Decl != nil {
			walkType(p.Decl.Type)
		}
		walkType(p.Type)
		for _, q := range p.Parts {
			walkTerm(q)
		}
	}
	for _, d := range m.Decls {
		walkType(d.Type)
	}
	for _, t := range m.Types {
		walkType(t)
	}
}

// wireSeq wires parts in order.
func wireSeq(s, e *st, parts []*Term) {
	cur := s
	for _, p := range parts {
		a, b := &st{}, &st{}
		wirePart(a, b, p)
		cur.eps = append(cur.eps, a)
		cur = b
	}
	cur.eps = append(cur.eps, e)
}

// wirePart wires s to e, accepting between p.Min and p.Max occurrences.
func wirePart(s, e *st, p *Term) {
	var one func(a, b *st)
	switch p.Kind {
	case Seq:
		one = func(a, b *st) { wireSeq(a, b, p.Parts) }
	case Choice:
		one = func(a, b *st) {
			for _, q := range p.Parts {
				x, y := &st{}, &st{}
				wirePart(x, y, q)
				a.eps = append(a.eps, x)
				y.eps = append(y.eps, b)
			}
		}
	default:
		// Elem, Any or Ref: one element, possibly repeated.
		one = func(a, b *st) {
			c := &st{term: p}
			a.eps = append(a.eps, c)
			c.eps = append(c.eps, b)
		}
	}
	wireOcc(s, e, p, one)
}

// wireOcc chains copies of one so that between p.Min and p.Max occurrences are
// accepted. A bounded part is unrolled, which keeps the counts exact; an
// unbounded one gets a loop instead, so a model with maxOccurs="unbounded"
// stays small without losing the mandatory minimum. Getting that minimum wrong
// here is what let a document with no invoice lines at all pass as well formed.
func wireOcc(s, e *st, p *Term, one func(a, b *st)) {
	lo, hi := p.Min, p.Max
	if lo < 0 {
		lo = 0
	}
	unbounded := hi < 0
	if unbounded {
		hi = lo + 1
	}
	if hi > maxExpand {
		hi = maxExpand
	}
	if hi < lo {
		hi = lo
	}
	if lo == 0 {
		s.eps = append(s.eps, e)
	}
	cur, lastA, lastB := s, (*st)(nil), (*st)(nil)
	for i := range hi {
		a, b := &st{}, &st{}
		one(a, b)
		cur.eps = append(cur.eps, a)
		if i+1 >= lo {
			b.eps = append(b.eps, e)
		}
		cur, lastA, lastB = b, a, b
	}
	if unbounded && lastB != nil {
		lastB.eps = append(lastB.eps, lastA)
	}
}

// fragFor returns the cached machine for a type's content.
func (t *Type) fragFor() *frag {
	if t.frag == nil {
		t.frag = compile(t.Term)
	}
	return t.frag
}

// Validate checks a parsed document against the model.
func (m *Model) Validate(d *xp.Doc) []Finding {
	root := d.RootElem()
	if root == nil {
		return []Finding{{Message: "document has no root element"}}
	}
	id := clark(root.NS, root.Name)
	decl, ok := m.Decls[id]
	if !ok {
		return []Finding{{
			Path:    "/" + root.Name,
			Message: fmt.Sprintf("root element %s is not declared by the %s schema", show(id), m.Source),
		}}
	}
	var out []Finding
	m.check(root, decl.Type, "/"+root.Name, &out)
	return out
}

// accept reports whether a term takes the given element. The term name is
// already in Clark notation, so it is split in place and compared part by
// part: building the candidate's Clark name for every comparison in every
// state set was costing more than the comparisons themselves.
func (t *Term) accept(ns, name string) bool {
	if t.Kind == Any {
		return true
	}
	if ns == "" {
		return t.Name == name
	}
	id := t.Name
	if id == "" || id[0] != '{' {
		return false
	}
	i := strings.IndexByte(id, '}')
	if i < 0 {
		return false
	}
	return id[1:i] == ns && id[i+1:] == name
}

// check validates the children of el against the content model of ct.
func (m *Model) check(el *xp.Node, ct *Type, path string, out *[]Finding) {
	if ct == nil || ct.Term == nil {
		if len(el.Children) > 0 {
			names := make([]string, 0, len(el.Children))
			for _, c := range el.Children {
				names = append(names, c.Name)
			}
			*out = append(*out, Finding{
				Path:    path,
				Message: fmt.Sprintf("%s takes a text value, but contains %s", show(el.Name), strings.Join(names, ", ")),
			})
		}
		return
	}
	f := ct.fragFor()
	cur := f.first
	for _, ch := range el.Children {
		var raw []*st
		var matched *Term
		for _, s := range cur {
			if s.term == nil || !s.term.accept(ch.NS, ch.Name) {
				continue
			}
			if matched == nil {
				matched = s.term
			}
			raw = append(raw, s.eps...)
		}
		if matched == nil {
			*out = append(*out, Finding{
				Path: path + "/" + ch.Name,
				Message: fmt.Sprintf("element %s is not allowed here; expected %s",
					ch.Name, expectList(cur)),
			})
			continue
		}
		cur = closure(raw)
		if matched.Kind == Any {
			// A wildcard stands for content the schema does not describe,
			// so its subtree is nobody's business here: descending into
			// it would report a foreign element's own children as defects.
			continue
		}
		m.check(ch, matched.content(), path+"/"+ch.Name, out)
	}
	if !contains(cur, f.end) {
		*out = append(*out, Finding{
			Path:    path,
			Message: fmt.Sprintf("missing required content: expected %s", expectList(cur)),
		})
	}
}

// content returns the content model of the accepted element: nil for a leaf.
func (t *Term) content() *Type {
	if t.Kind == Ref {
		return nil
	}
	return t.Type
}

// closure returns the epsilon closure of a state set. A consuming state is part
// of the set but its outgoing edges are not followed: an element has to match
// before the machine may move past it. Without that rule every reordering of a
// sequence looks legal, because the machine can drift forward through the
// elements it has not seen yet.
// closureStateMax is wider than the widest content model in either EN 16931
// syntax (the broadest UBL machine has 504 states), so the map path below is
// only a guard against a schema that grows.
const closureStateMax = 2048

// closureWords is one 64-bit word per 64 states of that bound.
const closureWords = closureStateMax / 64

func closure(in []*st) []*st {
	var bits [closureWords]uint64
	for _, s := range in {
		if s.idx >= closureStateMax {
			return closureMap(in)
		}
	}
	return closureBits(nil, &bits, in)
}

// closureBits walks the machine with a bitset of visited states on the stack:
// one zeroed array per check replaces the map the validator used to build for
// every element it looked at, and testing a bit costs less than scanning the
// states collected so far. The bits pointer keeps the array off the heap.
func closureBits(out []*st, bits *[closureWords]uint64, in []*st) []*st {
	for _, s := range in {
		w, b := s.idx>>6, uint(s.idx&63)
		if bits[w]&(1<<b) != 0 {
			continue
		}
		bits[w] |= 1 << b
		out = append(out, s)
		if s.term != nil {
			continue
		}
		out = closureBits(out, bits, s.eps)
	}
	return out
}

// closureMap is the guard for a machine wider than closureStateMax.
func closureMap(in []*st) []*st {
	var out []*st
	seen := map[*st]bool{}
	var visit func(s *st)
	visit = func(s *st) {
		if seen[s] {
			return
		}
		seen[s] = true
		out = append(out, s)
		if s.term != nil {
			return
		}
		for _, e := range s.eps {
			visit(e)
		}
	}
	for _, s := range in {
		visit(s)
	}
	return out
}

func contains(set []*st, want *st) bool {
	return slices.Contains(set, want)
}

// expectList turns a state set into the names it can still accept, which makes
// the message read like advice rather than a rejection.
func expectList(set []*st) string {
	var all, req []string
	seen, rseen := map[string]bool{}, map[string]bool{}
	var walk func(x *st, depth int)
	walk = func(x *st, depth int) {
		if depth > 4 {
			return
		}
		if x.term != nil {
			n := what(x.term)
			if !seen[n] {
				seen[n] = true
				all = append(all, n)
			}
			if x.term.Min > 0 && !rseen[n] {
				rseen[n] = true
				req = append(req, n)
			}
			return
		}
		for _, e := range x.eps {
			walk(e, depth+1)
		}
	}
	for _, s := range set {
		walk(s, 0)
	}
	list := req
	if len(list) == 0 {
		list = all
	}
	if len(list) == 0 {
		return "nothing more (the content model is exhausted here)"
	}
	sortNames(list)
	if len(list) > 12 {
		list = append(list[:12], fmt.Sprintf("... (%d in total)", len(list)))
	}
	return strings.Join(list, ", ")
}

// what renders a term for a message.
func what(t *Term) string {
	if t.Kind == Any {
		return "any element"
	}
	return show(t.Name)
}

// show drops the namespace from a Clark name, which only adds noise.
func show(id string) string {
	_, l := splitClark(id)
	if l == "" {
		return id
	}
	return l
}
