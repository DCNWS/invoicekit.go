package xsd

import (
	"fmt"
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
}

// frag is a compiled content model.
type frag struct {
	start, end *st
}

// compile builds a state machine that accepts exactly the sequences a content
// model allows. Repeated parts are compiled afresh so occurrence counts stay
// exact.
func compile(t *Term) *frag {
	s, e := &st{}, &st{}
	wirePart(s, e, t)
	return &frag{start: s, end: e}
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
	for i := 0; i < hi; i++ {
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

// accept reports whether a term takes the given element.
func (t *Term) accept(ns, name string) bool {
	if t.Kind == Any {
		return true
	}
	return t.Name == clark(ns, name)
}

// check validates the children of el against the content model of ct.
func (m *Model) check(el *xp.Node, ct *Type, path string, out *[]Finding) {
	if ct == nil || ct.Term == nil {
		if len(el.Children) > 0 {
			names := make([]string, 0, len(el.Children))
			for _, c := range el.Children {
				names = append(names, show(clark(c.NS, c.Name)))
			}
			*out = append(*out, Finding{
				Path:    path,
				Message: fmt.Sprintf("%s takes a text value, but contains %s", show(el.Name), strings.Join(names, ", ")),
			})
		}
		return
	}
	f := ct.fragFor()
	cur := closure([]*st{f.start})
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
				Path: path + "/" + show(clark(ch.NS, ch.Name)),
				Message: fmt.Sprintf("element %s is not allowed here; expected %s",
					show(clark(ch.NS, ch.Name)), expectList(cur)),
			})
			continue
		}
		cur = closure(raw)
		m.check(ch, matched.content(), path+"/"+show(clark(ch.NS, ch.Name)), out)
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
func closure(in []*st) []*st {
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
	for _, s := range set {
		if s == want {
			return true
		}
	}
	return false
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
