package main

// Targeted insertion mutants.
//
// The systematic sweep mutates what a document contains, so it can never fire
// an assert that forbids an element no reference document uses: "DurationMeasure
// shall not be used" has nothing to break. Those asserts stay unproven however
// many clean documents are mutated.
//
// This pass works the other way round. For an unproven assert shaped
// not(<element path>), it builds the demanded path inside a node matching the
// rule's context, tries every sibling position, and keeps the first variant
// that the XML schema still accepts and that makes that assert speak.

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"

	"github.com/dcnws/invoicekit.go/internal/en16931"
	"github.com/dcnws/invoicekit.go/internal/sch"
	"github.com/dcnws/invoicekit.go/xsd"
)

// qName is an element name, optionally prefixed, as paths and predicates write
// it. tagName additionally allows the characters a document's own names use.
const qName = `[A-Za-z][\w.-]*(?::[A-Za-z][\w.-]*)?`

var (
	prefixDecl  = regexp.MustCompile(`xmlns:([\w.-]+)="([^"]*)"`)
	defaultDecl = regexp.MustCompile(`\sxmlns="([^"]*)"`)
	tagNameRe   = regexp.MustCompile(`^[A-Za-z][\w:.-]*`)
	stepRe      = regexp.MustCompile(`^(` + qName + `)((?:\[[^]]*\])*)$`)
	predList    = regexp.MustCompile(`\[([^]]*)\]`)
	predLit     = regexp.MustCompile(`^((?:` + qName + `)(?:/(?:` + qName + `))*)\s*=\s*'([^']*)'$`)
	predName    = regexp.MustCompile(`^` + qName + `$`)
	notTest     = regexp.MustCompile(`^not\(([^()]+)\)$`)
	tagCut      = regexp.MustCompile(`<[^>]*>`)
)

// node is a byte range of the document, plus the name of the element it spans.
type node struct {
	name   string
	lo, hi int
}

// docPrefixes maps the namespace URIs declared on the document's root element
// to the prefixes that document uses for them, so schematron paths can be
// translated into names the document actually contains.
func docPrefixes(b []byte) map[string]string {
	out := map[string]string{}
	start := -1
	for i := range len(b) {
		if b[i] == '<' && i+1 < len(b) && isNameStart(b[i+1]) {
			start = i
			break
		}
	}
	if start < 0 {
		return out
	}
	gt, ok := tagEnd(b, start)
	if !ok {
		return out
	}
	tag := b[start:gt]
	for _, m := range prefixDecl.FindAllStringSubmatch(string(tag), -1) {
		out[m[2]] = m[1]
	}
	if m := defaultDecl.FindStringSubmatch(string(tag)); m != nil {
		out[m[1]] = ""
	}
	return out
}

// xlateQName maps a schematron QName onto the QName the document uses, leaving
// it unchanged when the document has no prefix for that namespace: such a step
// cannot match anyway, and translating it into a default namespace would match
// elements the rule never meant.
func xlateQName(q string, sch, doc map[string]string) string {
	p, loc, _ := strings.Cut(q, ":")
	if p != "" {
		if uri, ok := sch[p]; ok {
			if pre, ok := doc[uri]; ok {
				if pre == "" {
					return loc
				}
				return pre + ":" + loc
			}
		}
	}
	return q
}

// parsePath splits an element path into its steps. desc reports a descendant
// ('//') path. ok is false for anything that is not a plain element path, such
// as a path with an attribute step or a wildcard.
func parsePath(path string) (desc bool, steps []pathStep, ok bool) {
	if strings.Contains(path, "@") || strings.Contains(path, "*") {
		return false, nil, false
	}
	desc = strings.HasPrefix(path, "//")
	rest := path
	if desc {
		rest = path[2:]
	}
	for p := range strings.SplitSeq(rest, "/") {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		m := stepRe.FindStringSubmatch(p)
		if m == nil {
			return false, nil, false
		}
		steps = append(steps, pathStep{name: m[1], preds: m[2]})
	}
	if len(steps) == 0 {
		return false, nil, false
	}
	return desc, steps, true
}

type pathStep struct {
	name  string
	preds string
}

// extent returns the byte range of the first element named name at or after
// start, following elements of the same name nested inside it.
func extent(b []byte, name string, start int) (lo, hi int, ok bool) {
	lo, ok = findTag(b, name, start)
	if !ok {
		return 0, 0, false
	}
	depth, j := 0, lo
	for {
		t, ok := findTag(b, name, j)
		if !ok {
			return 0, 0, false
		}
		gt, ok := tagEnd(b, t)
		if !ok {
			return 0, 0, false
		}
		switch {
		case b[t+1] == '/':
			depth--
			if depth == 0 {
				return lo, gt + 1, true
			}
		case gt > t+1 && b[gt-1] == '/':
			if depth == 0 {
				return lo, gt + 1, true
			}
		default:
			depth++
		}
		j = gt + 1
	}
}

// findTag returns the index of the '<' opening a start or end tag named name,
// searching from start.
func findTag(b []byte, name string, start int) (int, bool) {
	n := []byte(name)
	for i := start; i < len(b); {
		lt := bytes.IndexByte(b[i:], '<')
		if lt < 0 {
			return 0, false
		}
		lt += i
		p := lt + 1
		if p < len(b) && b[p] == '/' {
			p++
		}
		if bytes.HasPrefix(b[p:], n) && tagBoundary(b, p+len(n)) {
			return lt, true
		}
		i = lt + 1
	}
	return 0, false
}

func tagBoundary(b []byte, i int) bool {
	if i >= len(b) {
		return false
	}
	return b[i] == ' ' || b[i] == '\t' || b[i] == '\n' || b[i] == '\r' || b[i] == '>' || b[i] == '/'
}

func isNameStart(c byte) bool {
	return c == '_' || (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z')
}

// tagEnd returns the index of the '>' that closes the tag starting at t,
// skipping quoted attribute values that may contain '>'.
func tagEnd(b []byte, t int) (int, bool) {
	for i := t + 1; i < len(b); i++ {
		switch b[i] {
		case '"', '\'':
			k := bytes.IndexByte(b[i+1:], b[i])
			if k < 0 {
				return 0, false
			}
			i += k + 1
		case '>':
			return i, true
		}
	}
	return 0, false
}

// children lists the direct children of the element spanning [lo,hi), in
// document order. Comments, processing instructions and text are skipped.
func children(b []byte, lo, hi int) []node {
	gt, ok := tagEnd(b, lo)
	if !ok || gt >= hi {
		return nil
	}
	var out []node
	i := lo
	if b[gt-1] != '/' {
		i = gt + 1
	}
	for i < hi {
		lt := bytes.IndexByte(b[i:hi], '<')
		if lt < 0 {
			break
		}
		lt += i
		if lt+1 >= len(b) {
			break
		}
		switch b[lt+1] {
		case '/': // our own end tag: nothing else can be a child
			return out
		case '!', '?':
			end := bytes.Index(b[lt:], []byte("-->"))
			if b[lt+1] == '?' {
				end = bytes.Index(b[lt:], []byte("?>"))
			}
			if end < 0 {
				return out
			}
			i = lt + end + 2
			if b[lt+1] == '!' {
				i++
			}
			continue
		}
		nm, ok := tagName(b, lt)
		if !ok {
			i = lt + 1
			continue
		}
		s, e, ok := extent(b, nm, lt)
		if !ok || e > hi {
			break
		}
		out = append(out, node{name: nm, lo: s, hi: e})
		i = e
	}
	return out
}

func tagName(b []byte, lt int) (string, bool) {
	if lt+1 >= len(b) {
		return "", false
	}
	m := tagNameRe.Find(b[lt+1:])
	if m == nil {
		return "", false
	}
	return string(m), true
}

// predOK reports whether the node satisfies every predicate of a path step.
// It understands [child], [a/b/c = 'literal'] and a conjunction of both;
// anything else, including a comparison on a node set, is refused, because a
// predicate that is quietly ignored would let a mutant be built in the wrong
// place.
func predOK(b []byte, n node, preds string, smap, dmap map[string]string) bool {
	for _, m := range predList.FindAllStringSubmatch(preds, -1) {
		pr := strings.TrimSpace(m[1])
		if lit := predLit.FindStringSubmatch(pr); lit != nil {
			lo, hi := n.lo, n.hi
			for q := range strings.SplitSeq(lit[1], "/") {
				nm := xlateQName(q, smap, dmap)
				c, ok := childNamed(b, lo, hi, nm)
				if !ok {
					return false
				}
				lo, hi = c.lo, c.hi
			}
			if strings.TrimSpace(tagCut.ReplaceAllString(string(b[lo:hi]), "")) != lit[2] {
				return false
			}
			continue
		}
		if predName.MatchString(pr) {
			if _, ok := childNamed(b, n.lo, n.hi, xlateQName(pr, smap, dmap)); !ok {
				return false
			}
			continue
		}
		return false
	}
	return true
}

func childNamed(b []byte, lo, hi int, name string) (node, bool) {
	for _, c := range children(b, lo, hi) {
		if c.name == name {
			return c, true
		}
	}
	return node{}, false
}

// walk returns the first node under [lo,hi) matching path.
func walk(b []byte, path string, lo, hi int, smap, dmap map[string]string) (node, bool) {
	desc, steps, ok := parsePath(path)
	if !ok {
		return node{}, false
	}
	for _, st := range steps {
		nm := xlateQName(st.name, smap, dmap)
		var found node
		var hit bool
		if desc {
			for i := lo; i < hi; {
				t, ok := findTag(b, nm, i)
				if !ok || t >= hi {
					break
				}
				s, e, ok := extent(b, nm, t)
				if !ok || e > hi {
					i = t + 1
					continue
				}
				cand := node{name: nm, lo: s, hi: e}
				if predOK(b, cand, st.preds, smap, dmap) {
					found, hit = cand, true
					break
				}
				i = t + 1
			}
		} else {
			for _, c := range children(b, lo, hi) {
				if c.name != nm {
					continue
				}
				if !predOK(b, c, st.preds, smap, dmap) {
					continue
				}
				found, hit = c, true
				break
			}
		}
		if !hit {
			return node{}, false
		}
		lo, hi = found.lo, found.hi
	}
	return foundNode(b, lo, hi)
}

// foundNode names the node the walk landed on, which costs one extra parse but
// keeps the caller's view of a node consistent with children's.
func foundNode(b []byte, lo, hi int) (node, bool) {
	nm, ok := tagName(b, lo)
	if !ok {
		return node{}, false
	}
	return node{name: nm, lo: lo, hi: hi}, true
}

// fragOf renders chain steps as literal XML, outermost first, with leaf as the
// content of the innermost element. A chain whose innermost element has no
// content is written self-closing.
func fragOf(steps []pathStep, smap, dmap map[string]string, leaf []byte) []byte {
	var out []byte
	for i := range slices.Backward(steps) {
		n := xlateQName(steps[i].name, smap, dmap)
		inner := out
		if inner == nil {
			inner = leaf
		}
		if len(inner) == 0 {
			out = append([]byte("<"+n+"/>"), inner...)
		} else {
			out = append(append([]byte("<"+n+">"), inner...), []byte("</"+n+">")...)
		}
	}
	return out
}

// fragsOf renders a chain as the schema model describes it when it can, then
// the two blind guesses: placeholder text at the leaf, and an empty leaf. Which
// one the schema accepts is a property of the leaf's type, not of the assert,
// and the schematron paths do not say whether an element holds text or children.
func fragsOf(m *xsd.Model, steps []pathStep, smap, dmap map[string]string) [][]byte {
	var out [][]byte
	if f := schemaFrag(m, smap, dmap, steps); f != nil {
		out = append(out, f)
	}
	return append(out,
		fragOf(steps, smap, dmap, []byte("x")),
		fragOf(steps, smap, dmap, nil))
}

// maxSchemaDepth bounds how deep the schema model is followed when filling in
// the content an inserted element requires.
const maxSchemaDepth = 8

// schemaFrag renders the chain steps as literal XML with the content the XML
// schema requires of each element, so an insertion validates instead of guessing
// at what the leaf holds. It refuses a chain the model does not describe, and a
// step carrying a predicate, which a content model cannot express.
func schemaFrag(m *xsd.Model, smap, dmap map[string]string, steps []pathStep) []byte {
	if m == nil {
		return nil
	}
	var names []string
	// cur is the content model of the element built so far: nil means its
	// type is a simple value, or (before the first step) that nothing has
	// been resolved yet.
	var cur *xsd.Term
	for i, st := range steps {
		if st.preds != "" {
			return nil
		}
		c, ok := clarkName(st.name, smap)
		if !ok {
			return nil
		}
		names = append(names, c)
		if i == 0 {
			d := m.Decls[c]
			if d == nil {
				return nil
			}
			if d.Type != nil {
				cur = d.Type.Term
			}
			continue
		}
		if cur == nil {
			return nil
		}
		el := findElem(cur, c)
		if el == nil {
			return nil
		}
		cur = el.Content()
	}
	var inner []byte
	if cur != nil {
		b, ok := partsOf(cur, dmap, 0)
		if !ok {
			return nil
		}
		inner = b
	} else {
		inner = []byte("x")
	}
	return wrapNames(names, dmap, inner)
}

// clarkName names a prefixed element in Clark notation, {namespace}local, which
// is how the schema model keys its components.
func clarkName(q string, smap map[string]string) (string, bool) {
	p, loc, found := strings.Cut(q, ":")
	if !found || p == "" {
		return "", false
	}
	uri, ok := smap[p]
	if !ok {
		return "", false
	}
	return "{" + uri + "}" + loc, true
}

// prefixed names a Clark element with the prefix the document uses for its
// namespace, so an inserted element needs no new namespace declaration.
func prefixed(clark string, dmap map[string]string) (string, bool) {
	uri, loc := "", clark
	if strings.HasPrefix(clark, "{") {
		i := strings.Index(clark, "}")
		if i < 0 {
			return "", false
		}
		uri, loc = clark[1:i], clark[i+1:]
	}
	pre, ok := dmap[uri]
	if !ok {
		return "", false
	}
	if pre == "" {
		return loc, true
	}
	return pre + ":" + loc, true
}

// findElem returns the term of the content model that accepts the element,
// searching sequences and choices but not the elements' own content.
func findElem(t *xsd.Term, clark string) *xsd.Term {
	if t == nil {
		return nil
	}
	if t.Kind == xsd.Elem {
		if t.Name == clark {
			return t
		}
		return nil
	}
	for _, p := range t.Parts {
		if el := findElem(p, clark); el != nil {
			return el
		}
	}
	return nil
}

// partsOf renders the parts of a content model that the schema requires: every
// part of a sequence with a minimum of one, and the first branch of a choice
// that can be rendered. An optional part is left out, and an xs:any that the
// schema requires cannot be filled in, so the chain is refused.
func partsOf(t *xsd.Term, dmap map[string]string, depth int) ([]byte, bool) {
	if depth > maxSchemaDepth {
		return nil, false
	}
	switch t.Kind {
	case xsd.Elem:
		return elemOf(t, dmap, depth)
	case xsd.Any, xsd.Ref:
		return nil, t.Min == 0
	}
	if t.Kind == xsd.Choice && t.Min == 0 {
		return nil, true
	}
	var out []byte
	for _, p := range t.Parts {
		if p.Min == 0 {
			continue
		}
		b, ok := partsOf(p, dmap, depth+1)
		if ok {
			out = append(out, b...)
			continue
		}
		if t.Kind == xsd.Choice {
			continue
		}
		return nil, false
	}
	if t.Kind == xsd.Choice && len(out) == 0 {
		return nil, false
	}
	return out, true
}

// elemOf renders one element with the content its type requires: a placeholder
// value when its type has no element children.
func elemOf(t *xsd.Term, dmap map[string]string, depth int) ([]byte, bool) {
	name, ok := prefixed(t.Name, dmap)
	if !ok {
		return nil, false
	}
	ct := t.Content()
	if ct == nil {
		return []byte("<" + name + ">x</" + name + ">"), true
	}
	inner, ok := partsOf(ct, dmap, depth)
	if !ok {
		return nil, false
	}
	return wrap([]byte(name), inner), true
}

// wrapNames nests the named elements around inner, names[0] outermost.
func wrapNames(names []string, dmap map[string]string, inner []byte) []byte {
	out := inner
	for i := range slices.Backward(names) {
		nm, ok := prefixed(names[i], dmap)
		if !ok {
			return nil
		}
		out = wrap([]byte(nm), out)
	}
	return out
}

// wrap puts content inside an element, self-closing it when there is none.
func wrap(name, content []byte) []byte {
	if len(content) == 0 {
		return append([]byte("<"+string(name)+"/>"), content...)
	}
	out := append([]byte("<"+string(name)+">"), content...)
	return append(out, []byte("</"+string(name)+">")...)
}

// childStep returns the child of n named and predicated like one chain step.
func childStep(b []byte, n node, st pathStep, smap, dmap map[string]string) (node, bool) {
	nm := xlateQName(st.name, smap, dmap)
	for _, c := range children(b, n.lo, n.hi) {
		if c.name == nm && predOK(b, c, st.preds, smap, dmap) {
			return c, true
		}
	}
	return node{}, false
}

// insert renders f as a child of n at every position a sibling could occupy,
// keeping the indentation of the following line so the corpus stays readable.
func insert(b []byte, n node, f []byte) [][]byte {
	gt, ok := tagEnd(b, n.lo)
	if !ok || gt >= n.hi {
		return nil
	}
	if b[gt-1] == '/' {
		nm, ok := tagName(b, n.lo)
		if !ok {
			return nil
		}
		out := append([]byte{}, b[:gt-1]...)
		out = append(out, '>')
		out = append(out, f...)
		out = append(out, []byte("</"+nm+">")...)
		return [][]byte{append(out, b[gt+1:]...)}
	}
	kids := children(b, n.lo, n.hi)
	spots := make([]int, 0, len(kids)+1)
	for _, c := range kids {
		spots = append(spots, c.lo)
	}
	if at := bytes.LastIndex(b[n.lo:n.hi], []byte("</")); at >= 0 {
		spots = append(spots, n.lo+at)
	}
	out := make([][]byte, 0, len(spots))
	for _, at := range spots {
		if at <= gt {
			continue
		}
		pad := b[bytes.LastIndexByte(b[:at], '\n')+1 : at]
		var v []byte
		v = append(v, b[:at]...)
		v = append(v, f...)
		if strings.TrimSpace(string(pad)) == "" {
			v = append(v, '\n')
			v = append(v, pad...)
		}
		out = append(out, append(v, b[at:]...))
	}
	return out
}

// targetRule is one unproven assert this pass can attack: its test forbids a
// plain element path, so building that path is what makes it speak.
type targetRule struct {
	id    string
	ctx   string
	chain string
}

// targetRules picks the unproven asserts of a rule set whose test is exactly
// not(<element path>).
func targetRules(s *sch.Schema, want map[string]bool) []targetRule {
	var out []targetRule
	for _, pat := range s.Patterns {
		for _, r := range pat.Rules {
			for _, a := range r.Asserts {
				if a.Report || !want[a.ID] {
					continue
				}
				m := notTest.FindStringSubmatch(strings.TrimSpace(a.Test))
				if m == nil {
					continue
				}
				chain := strings.TrimSpace(m[1])
				if _, _, ok := parsePath(chain); !ok {
					continue
				}
				out = append(out, targetRule{id: a.ID, ctx: r.Context, chain: chain})
			}
		}
	}
	return out
}

// targetStats counts what the targeted pass did.
type targetStats struct {
	rules  int
	made   int
	nofire int
}

// targeted harvests one insertion mutant per rule in want, marking every
// assert the mutants fired as proven.
func targeted(sy en16931.Syntax, doc []byte, want map[string]bool, n int, corpus string, u *universe) (targetStats, error) {
	s, err := en16931.Schematron(sy)
	if err != nil {
		return targetStats{}, err
	}
	smap, dmap := s.NS, docPrefixes(doc)
	m, err := en16931.SchemaModel(sy)
	if err != nil {
		return targetStats{}, err
	}
	var st targetStats
	for _, r := range targetRules(s, want) {
		st.rules++
		variants := variantsAt(m, doc, r.ctx, r.chain, smap, dmap)
		if len(variants) == 0 {
			continue
		}
		keep, ids := firstFiring(sy, r.id, variants, workers(n))
		if keep == nil {
			st.nofire++
			continue
		}
		name := fmt.Sprintf("%s--ins-%s-under-%s.xml", r.id, sanitize(leaf(r.chain)), sanitize(leaf(r.ctx)))
		dir := filepath.Join(corpus, string(sy))
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return st, err
		}
		if err := os.WriteFile(filepath.Join(dir, name), keep, 0o644); err != nil {
			return st, err
		}
		u.mark(sy, ids, source{Kind: "insertion-mutant", What: name})
		st.made++
	}
	return st, nil
}

// variantsAt builds the insertion variants of chain under the first context
// alternative the document contains. The chain is grown where it runs out, not
// bolted onto the context: most of the elements these asserts name allow one
// copy only, so a document that already has the Delivery must gain the missing
// tail inside that Delivery, and adding a second one is what the schema refuses.
//
// The tail that is missing is rendered from the schema's own content model
// where that model describes it, so what goes in carries the elements its type
// demands. The placeholder and empty guesses follow, for chains the model
// cannot express: a step carrying a predicate, or a component the model has no
// declaration for.
func variantsAt(m *xsd.Model, doc []byte, ctx, chain string, smap, dmap map[string]string) [][]byte {
	_, steps, ok := parsePath(chain)
	if !ok {
		return nil
	}
	for alt := range strings.SplitSeq(ctx, "|") {
		n, ok := walk(doc, strings.TrimSpace(alt), 0, len(doc), smap, dmap)
		if !ok {
			continue
		}
		k := 0
		for k < len(steps) {
			c, ok := childStep(doc, n, steps[k], smap, dmap)
			if !ok {
				break
			}
			n, k = c, k+1
		}
		if rest := steps[k:]; k < len(steps) && len(rest) <= 4 {
			var vs [][]byte
			for _, f := range fragsOf(m, rest, smap, dmap) {
				vs = append(vs, insert(doc, n, f)...)
			}
			if len(vs) > 0 {
				return vs
			}
		}
	}
	return nil
}

// firstFiring validates the variants in parallel and returns the earliest one,
// in the order they were built, that the schema accepts, that every rule can be
// evaluated against, and that makes id speak: earliest so that one run of the
// pass reproduces the corpus it produced before, whatever order the workers
// happen to finish in. A mutant that is schema invalid
// proves nothing: the reference implementations stop at the schema, and so do
// the replay tests over this corpus.
func firstFiring(sy en16931.Syntax, id string, docs [][]byte, n int) ([]byte, []string) {
	type res struct {
		i   int
		ids []string
		ok  bool
	}
	in := make(chan int)
	out := make(chan res, 8)
	var wg sync.WaitGroup
	for range min(n, len(docs)) {
		wg.Go(func() {
			for i := range in {
				ids, ok := firingIDs(sy, id, docs[i])
				out <- res{i, ids, ok}
			}
		})
	}
	go func() {
		for i := range docs {
			in <- i
		}
		close(in)
		wg.Wait()
		close(out)
	}()
	best, ids := -1, []string(nil)
	for r := range out {
		if !r.ok || (best >= 0 && r.i >= best) {
			continue
		}
		best, ids = r.i, r.ids
	}
	if best < 0 {
		return nil, nil
	}
	return docs[best], ids
}

// firingIDs validates doc and returns every assert it fires, reporting true
// only when the schema accepted the document, every rule could be evaluated
// against it, and id is among the asserts that spoke.
func firingIDs(sy en16931.Syntax, id string, doc []byte) ([]string, bool) {
	rep, err := en16931.Validate(doc, en16931.Options{Syntax: sy})
	if err != nil || rep == nil || len(rep.Schema) != 0 || rep.Internal != 0 {
		return nil, false
	}
	fired := firedIDs(rep)
	if !slices.Contains(fired, id) {
		return nil, false
	}
	return fired, true
}

// leaf names the last element of a path without its namespace prefix or
// predicates, which is what corpus file names use.
func leaf(q string) string {
	p, _, _ := strings.Cut(q, "|")
	if i := strings.LastIndex(p, "/"); i >= 0 {
		p = p[i+1:]
	}
	if i := strings.LastIndex(p, ":"); i >= 0 {
		p = p[i+1:]
	}
	if i := strings.Index(p, "["); i >= 0 {
		p = p[:i]
	}
	return strings.TrimSpace(p)
}

// targetedBase picks the clean document the targeted pass mutates. The
// repository's own samples are the ones the harvested corpus was built from, so
// they come first; a reference document of the right syntax is the fallback.
func targetedBase(refs []string, sy en16931.Syntax) ([]byte, error) {
	sample := filepath.Join("testdata", "samples", map[string]string{
		string(en16931.SyntaxCII): "cii-xrechnung.xml",
		string(en16931.SyntaxUBL): "ubl-peppol.xml",
	}[string(sy)])
	if b, err := os.ReadFile(sample); err == nil {
		return b, nil
	}
	for _, f := range refs {
		b, err := os.ReadFile(f)
		if err != nil {
			return nil, err
		}
		got, err := en16931.Detect(b)
		if err != nil || got != sy {
			continue
		}
		return b, nil
	}
	return nil, fmt.Errorf("no %s document to build targeted mutants from", sy)
}
