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

// frag renders an element path as literal XML, outermost element first, with a
// placeholder text node at the leaf. Chains deeper than four elements are
// refused: they need a whole subtree of context to stay schema valid, and
// inventing that is the job of a targeted rule, not of this one.
func frag(chain string, smap, dmap map[string]string) ([]byte, bool) {
	_, steps, ok := parsePath(chain)
	if !ok || len(steps) > 4 {
		return nil, false
	}
	var out []byte
	for i := range slices.Backward(steps) {
		n := xlateQName(steps[i].name, smap, dmap)
		inner := out
		out = append([]byte("<"+n+">"), inner...)
		if inner == nil {
			out = append(out, 'x')
		}
		out = append(out, []byte("</"+n+">")...)
	}
	return out, true
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
	var st targetStats
	for _, r := range targetRules(s, want) {
		f, ok := frag(r.chain, smap, dmap)
		if !ok {
			continue
		}
		st.rules++
		variants := variantsAt(doc, r.ctx, f, smap, dmap)
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

// variantsAt builds the insertion variants under the first context alternative
// that is present in the document.
func variantsAt(doc []byte, ctx string, f []byte, smap, dmap map[string]string) [][]byte {
	for alt := range strings.SplitSeq(ctx, "|") {
		n, ok := walk(doc, strings.TrimSpace(alt), 0, len(doc), smap, dmap)
		if !ok {
			continue
		}
		if vs := insert(doc, n, f); len(vs) > 0 {
			return vs
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
		rep *en16931.Report
		err error
	}
	in := make(chan int)
	out := make(chan res, 8)
	var wg sync.WaitGroup
	for range min(n, len(docs)) {
		wg.Go(func() {
			for i := range in {
				rep, err := en16931.Validate(docs[i], en16931.Options{Syntax: sy})
				out <- res{i, rep, err}
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
		if r.err != nil || r.rep == nil || (best >= 0 && r.i >= best) {
			continue
		}
		if len(r.rep.Schema) != 0 || r.rep.Internal != 0 {
			continue
		}
		fired := firedIDs(r.rep)
		if !slices.Contains(fired, id) {
			continue
		}
		best, ids = r.i, fired
	}
	if best < 0 {
		return nil, nil
	}
	return docs[best], ids
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
