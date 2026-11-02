// Command cov measures assert-level coverage of the EN 16931 Schematron rule
// sets: for every valid reference document it generates systematic mutants
// (deleted nodes, emptied elements, garbage values, removed attributes,
// duplicated nodes) and records which asserts actually fire.
//
//	cov [-corpus testdata/mutations] [-bases list] [-report cov-report.md]
//
// Mutants that introduce a previously uncovered finding are written to the
// corpus together with a manifest, so a replay test can lock the rules in.
package main

import (
	"bytes"
	"cmp"
	"encoding/json"
	"encoding/xml"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"sync"

	"github.com/dcnws/invoicekit.go/internal/en16931"
	"github.com/dcnws/invoicekit.go/internal/sch"
	"github.com/dcnws/invoicekit.go/internal/xp"
)

type span struct {
	name       string
	tagStart   int // index of '<' opening the element
	startEnd   int // index just past the start tag (or the whole self-closing tag)
	end        int // index just past the end tag (== startEnd when self-closing)
	contentEnd int // index of '<' opening the end tag
	parent     string
	attrs      []attrSpan
}

type attrSpan struct {
	name       string
	valueStart int
	valueEnd   int
	tagStart   int
	tagEnd     int
}

// spans walks b and returns every element with the offsets needed for text
// level edits. offsets come from xml.Decoder.InputOffset.
func spans(b []byte) []*span {
	dec := xml.NewDecoder(bytes.NewReader(b))
	var (
		out   []*span
		stack []*span
	)
	for {
		tk, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			break
		}
		switch t := tk.(type) {
		case xml.StartElement:
			off := int(dec.InputOffset())
			start := bytes.LastIndexByte(b[:off], '<')
			if start < 0 {
				continue
			}
			sp := &span{name: t.Name.Local, tagStart: start, startEnd: off, end: off}
			if len(stack) > 0 {
				sp.parent = stack[len(stack)-1].name
			}
			tag := b[start:off]
			for _, a := range t.Attr {
				if strings.HasPrefix(a.Name.Local, "xmlns") || strings.Contains(a.Name.Local, ":") {
					continue // namespace declarations and prefixed attributes
				}
				if as, ok := findAttr(tag, a.Name.Local, start); ok {
					sp.attrs = append(sp.attrs, as)
				}
			}
			stack = append(stack, sp)
			out = append(out, sp)
		case xml.EndElement:
			off := int(dec.InputOffset())
			for i := range slices.Backward(stack) {
				if stack[i].name == t.Name.Local {
					stack[i].end = off
					if stack[i].end > stack[i].startEnd {
						stack[i].contentEnd = bytes.LastIndexByte(b[:off], '<')
					}
					stack = stack[:i]
					break
				}
			}
		}
	}
	return out
}

func findAttr(tag []byte, name string, base int) (attrSpan, bool) {
	n := []byte(name)
	for i := 0; i+len(n)+2 <= len(tag); i++ {
		if !bytes.HasPrefix(tag[i:], n) {
			continue
		}
		if i > 0 {
			c := tag[i-1]
			if c != ' ' && c != '\t' && c != '\n' && c != '\r' {
				continue
			}
		}
		j := i + len(n)
		for j < len(tag) && (tag[j] == ' ' || tag[j] == '\t' || tag[j] == '\n' || tag[j] == '\r') {
			j++
		}
		if j >= len(tag) || tag[j] != '=' {
			continue
		}
		j++
		for j < len(tag) && (tag[j] == ' ' || tag[j] == '\t') {
			j++
		}
		if j >= len(tag) || (tag[j] != '"' && tag[j] != '\'') {
			continue
		}
		q := tag[j]
		k := bytes.IndexByte(tag[j+1:], q)
		if k < 0 {
			continue
		}
		return attrSpan{
			name:       name,
			valueStart: base + j + 1,
			valueEnd:   base + j + 1 + k,
			tagStart:   base + i,
			tagEnd:     base + j + 2 + k,
		}, true
	}
	return attrSpan{}, false
}

type mutant struct {
	name string
	data []byte
}

// mutate returns a deduplicated set of single-edit variants of b.
func mutate(b []byte, root string) []mutant {
	sp := spans(b)
	// count siblings by name for the "delete one of a repeated group" probe
	count := map[string]map[string]int{}
	for _, s := range sp {
		if s.parent == "" {
			continue
		}
		if count[s.parent] == nil {
			count[s.parent] = map[string]int{}
		}
		count[s.parent][s.name]++
	}

	seen := map[string]bool{}
	var out []mutant
	add := func(name string, edit func(*strings.Builder)) {
		var sb strings.Builder
		edit(&sb)
		d := []byte(sb.String())
		if bytes.Equal(d, b) || seen[string(d)] {
			return
		}
		seen[string(d)] = true
		out = append(out, mutant{name, d})
	}

	// splice writes b with [from,to) replaced by rep
	splice := func(sb *strings.Builder, from, to int, rep string) {
		sb.Write(b[:from])
		sb.WriteString(rep)
		sb.Write(b[to:])
	}

	for _, s := range sp {
		if s.name == root {
			continue
		}
		selfClosing := s.end == s.startEnd
		if !selfClosing {
			add(s.name+"/del", func(sb *strings.Builder) { splice(sb, s.tagStart, s.end, "") })
			add(s.name+"/empty", func(sb *strings.Builder) { splice(sb, s.startEnd, s.contentEnd, "") })
			for i, v := range []string{"zz", "0", "-1", "2001-13-01", "99999"} {
				add(fmt.Sprintf("%s/text%d", s.name, i), func(sb *strings.Builder) {
					splice(sb, s.startEnd, s.contentEnd, v)
				})
			}
		} else {
			add(s.name+"/del", func(sb *strings.Builder) { splice(sb, s.tagStart, s.end, "") })
		}

		for _, a := range s.attrs {
			add(s.name+"/attr-"+a.name+"/junk", func(sb *strings.Builder) {
				sb.Write(b[:a.valueStart])
				sb.WriteString("zz")
				sb.Write(b[a.valueEnd:])
			})
			add(s.name+"/attr-"+a.name+"/del", func(sb *strings.Builder) {
				start := a.tagStart
				for start > s.tagStart && b[start-1] != ' ' && b[start-1] != '\n' && b[start-1] != '\t' && b[start-1] != '\r' {
					start--
				}
				sb.Write(b[:start])
				sb.Write(b[a.tagEnd:])
			})
		}

		// duplicate a node: fires "must not occur more than once" rules
		if s.name != "Invoice" && s.name != "CrossIndustryInvoice" && !selfClosing {
			add(s.name+"/dup", func(sb *strings.Builder) {
				sb.Write(b[:s.end])
				sb.Write(b[s.tagStart:s.end])
				sb.Write(b[s.end:])
			})
		}

		// delete one node of a repeated group, keeping at least one
		if s.parent != "" && count[s.parent][s.name] > 1 && !selfClosing {
			add(s.name+"/del-one-of-repeat", func(sb *strings.Builder) {
				splice(sb, s.tagStart, s.end, "")
			})
		}
	}
	return out
}

type source struct {
	Kind string `json:"kind"` // official-testset, full-document, mutant
	What string `json:"what"`
}

type job struct {
	base string
	sy   en16931.Syntax
	m    mutant
	// idx is the mutant's position in mutate's output for base. Mutant names
	// are only unique per element name, and an element name repeats, so a
	// mutant that has to be rebuilt after the sweep comes back by position.
	idx int
}

type outcome struct {
	job job
	res *en16931.Report
	err error
}

// payload is one document instance inside an official VEFA test set.
type payload struct {
	file     string
	idx      int
	sy       en16931.Syntax
	doc      *xp.Doc
	mustFire []string
	mustOK   []string
}

// testSets extracts every payload document from the CEF unit test sets. Each
// <test> carries expectations: <error>BR-x</error> means the rule must fire,
// <success>BR-x</success> means it must not.
func testSets(dirs []string) ([]payload, error) {
	var out []payload
	for _, d := range dirs {
		files, _ := filepath.Glob(filepath.Join(d, "*.xml"))
		sort.Strings(files)
		for _, f := range files {
			b, err := os.ReadFile(f)
			if err != nil {
				return nil, err
			}
			dec := xml.NewDecoder(bytes.NewReader(b))
			var fire, ok []string
			idx := 0
			for {
				tk, err := dec.Token()
				if err == io.EOF {
					break
				}
				if err != nil {
					return nil, fmt.Errorf("%s: %v", f, err)
				}
				se, ok2 := tk.(xml.StartElement)
				if !ok2 {
					continue
				}
				switch se.Name.Local {
				case "test":
					fire, ok = nil, nil
				case "error", "success":
					var id string
					if err := dec.DecodeElement(&id, &se); err != nil {
						return nil, fmt.Errorf("%s: %v", f, err)
					}
					if se.Name.Local == "error" {
						fire = append(fire, id)
					} else {
						ok = append(ok, id)
					}
				case "Invoice", "CreditNote", "CrossIndustryInvoice":
					doc, err := xp.ParseElement(dec, se)
					if err != nil {
						return nil, fmt.Errorf("%s: %v", f, err)
					}
					sy := en16931.SyntaxCII
					if se.Name.Local != "CrossIndustryInvoice" {
						sy = en16931.SyntaxUBL
					}
					idx++
					out = append(out, payload{file: f, idx: idx, sy: sy, doc: doc,
						mustFire: fire, mustOK: ok})
				}
			}
		}
	}
	return out, nil
}

// universe is the set of asserts a default validation run can reach, which is
// what coverage is measured against, together with the proofs found so far.
type universe struct {
	asserts  map[string]map[string]string   // syntax -> id -> flag
	pattern  map[string]map[string]string   // syntax -> id -> pattern id
	phases   map[string]map[string][]string // syntax -> id -> phases
	patCount map[string]map[string]int      // syntax -> pattern -> asserts
	covered  map[string]map[string]source   // syntax -> id -> first proven source
	phaseIDs map[string][]string            // syntax -> default phase ids
}

func newUniverse() *universe {
	return &universe{
		asserts:  map[string]map[string]string{},
		pattern:  map[string]map[string]string{},
		phases:   map[string]map[string][]string{},
		patCount: map[string]map[string]int{},
		covered:  map[string]map[string]source{},
		phaseIDs: map[string][]string{},
	}
}

// mark records that ids have been proven, and reports how many of them were
// proven for the first time: a mutant is only worth harvesting when it proves
// something new.
func (u *universe) mark(sy en16931.Syntax, ids []string, src source) int {
	var n int
	for _, id := range ids {
		if _, ok := u.covered[string(sy)][id]; !ok {
			u.covered[string(sy)][id] = src
			n++
		}
	}
	return n
}

// loadSyntax adds one syntax's asserts to the universe. A default validation
// run selects no phase, which per ISO Schematron means every pattern is
// active, including patterns no <phase> references: the UBL schema keeps its
// UBL-SR-* structural rules in such a pattern, so they must be counted.
func (u *universe) loadSyntax(sy en16931.Syntax) error {
	s, err := en16931.Schematron(sy)
	if err != nil {
		return err
	}
	key := string(sy)
	u.asserts[key] = map[string]string{}
	u.pattern[key] = map[string]string{}
	u.phases[key] = map[string][]string{}
	u.patCount[key] = map[string]int{}
	u.covered[key] = map[string]source{}
	u.phaseIDs[key] = s.PhaseIDs()

	byID := make(map[string]*sch.Pattern, len(s.Patterns))
	for _, pat := range s.Patterns {
		byID[pat.ID] = pat
	}
	referenced := map[string]bool{}
	for _, ph := range s.Phases {
		for _, pid := range ph.Patterns {
			referenced[pid] = true
			u.addPhase(key, byID[pid], ph.ID)
		}
	}
	for _, pat := range s.Patterns {
		if !referenced[pat.ID] {
			fmt.Fprintf(os.Stderr, "%s: pattern %s (%d asserts) is referenced by no phase but runs by default\n",
				sy, pat.ID, countAsserts(pat))
		}
		for _, r := range pat.Rules {
			for _, a := range r.Asserts {
				u.asserts[key][a.ID] = a.Flag
				u.pattern[key][a.ID] = pat.ID
				u.patCount[key][pat.ID]++
			}
		}
	}
	return nil
}

// addPhase notes that phID is one of the phases asserting through pat, listing
// a phase at most once per assert.
func (u *universe) addPhase(key string, pat *sch.Pattern, phID string) {
	if pat == nil {
		return
	}
	for _, r := range pat.Rules {
		for _, a := range r.Asserts {
			if !slices.Contains(u.phases[key][a.ID], phID) {
				u.phases[key][a.ID] = append(u.phases[key][a.ID], phID)
			}
		}
	}
}

func countAsserts(pat *sch.Pattern) int {
	var n int
	for _, r := range pat.Rules {
		n += len(r.Asserts)
	}
	return n
}

// coverage is the universe summarised: what was proven, by what, and which
// asserts nothing ever fired.
type coverage struct {
	totals  map[string]int
	missing map[string][]string
	bySrc   map[string]map[string]int
	hits    map[string]map[string]int // syntax -> pattern -> proven asserts
}

func (u *universe) coverage() coverage {
	cov := coverage{
		totals:  map[string]int{},
		missing: map[string][]string{},
		bySrc:   map[string]map[string]int{},
		hits:    map[string]map[string]int{},
	}
	for sy, m := range u.asserts {
		cov.totals[sy] = len(m)
		cov.bySrc[sy] = map[string]int{}
		cov.hits[sy] = map[string]int{}
		for id := range m {
			if _, ok := u.covered[sy][id]; !ok {
				cov.missing[sy] = append(cov.missing[sy], id)
				continue
			}
			cov.hits[sy][u.pattern[sy][id]]++
		}
		sort.Strings(cov.missing[sy])
		for _, s := range u.covered[sy] {
			cov.bySrc[sy][s.Kind]++
		}
	}
	return cov
}

// testSetStats counts what the official CEF unit test sets proved.
type testSetStats struct {
	cases    int
	expect   int
	missed   int
	missList []string
}

// corpusStats counts what replaying the harvested corpus proved.
type corpusStats struct {
	files    int
	expect   int
	missed   int
	evalErrs int
	missList []string
}

// mutantStats counts what the mutation sweep did.
type mutantStats struct {
	generated int
	hits      int
	rejected  int
	why       map[string]int
	example   map[string]string
}

// runStats bundles the three passes so a report needs one argument, not four.
type runStats struct {
	ts   testSetStats
	cr   corpusStats
	ms   mutantStats
	tg   map[string]targetStats // set only when -targeted ran
	refs int
}

// syntaxNames is the reporting order. Maps iterate unpredictably, and a report
// whose sections move between runs is impossible to diff.
var syntaxNames = []string{"CII", "UBL"}

// firedIDs returns the distinct rule ids a report contains, sorted.
func firedIDs(rep *en16931.Report) []string {
	seen := map[string]bool{}
	var out []string
	for _, f := range rep.Findings {
		if !seen[f.ID] {
			seen[f.ID] = true
			out = append(out, f.ID)
		}
	}
	sort.Strings(out)
	return out
}

// setOf turns ids into a membership test.
func setOf(ids []string) map[string]bool {
	set := make(map[string]bool, len(ids))
	for _, id := range ids {
		set[id] = true
	}
	return set
}

// runTestSets validates every payload document in the official test sets and
// checks the expectations each <test> states about it.
func runTestSets(ref string, u *universe) (testSetStats, error) {
	dirs := []string{
		filepath.Join(ref, "test", "Invoice-unit-UBL"),
		filepath.Join(ref, "test", "CreditNote-unit-UBL"),
		filepath.Join(ref, "test", "cii"),
	}
	pts, err := testSets(dirs)
	if err != nil {
		return testSetStats{}, err
	}
	var st testSetStats
	for _, p := range pts {
		rep, err := en16931.ValidateDoc(p.sy, p.doc, nil)
		if err != nil {
			fmt.Fprintf(os.Stderr, "%s: %v\n", p.file, err)
			continue
		}
		st.cases++
		ids := firedIDs(rep)
		fired := setOf(ids)
		what := filepath.Base(p.file) + "#" + fmt.Sprint(p.idx)
		u.mark(p.sy, ids, source{Kind: "official-testset", What: what})
		for _, id := range p.mustFire {
			st.expect++
			if fired[id] {
				continue
			}
			st.missed++
			if len(st.missList) < 40 {
				st.missList = append(st.missList, fmt.Sprintf("%s %s (%s#%d)", p.sy, id, filepath.Base(p.file), p.idx))
			}
		}
	}
	return st, nil
}

// replayCorpus re-checks the mutants an earlier run harvested. The ids each one
// is expected to fire are encoded in its file name, so this needs no separate
// bookkeeping: it proves the corpus still shows what it showed when it was
// harvested, and lets -no-mutate report full coverage in a couple of seconds.
func replayCorpus(corpus string, u *universe) (corpusStats, error) {
	var st corpusStats
	for _, sy := range []en16931.Syntax{en16931.SyntaxCII, en16931.SyntaxUBL} {
		files, err := filepath.Glob(filepath.Join(corpus, string(sy), "*.xml"))
		if err != nil {
			return st, err
		}
		sort.Strings(files)
		man, err := manifestSources(corpus, sy)
		if err != nil {
			return st, err
		}
		for _, f := range files {
			if err := replayMutant(f, sy, man, u, &st); err != nil {
				return st, err
			}
		}
	}
	return st, nil
}

// manifestSources reads back the provenance a previous run recorded, keyed by
// assert id. The manifest says which pass produced a mutant, and a replay
// cannot re-derive that: the targeted pass only works on asserts that are still
// unproven, so an assert the corpus already proves is never rebuilt and its
// origin would be lost. Entries written by a replay itself are skipped, since
// they carry no more information than the file's own name.
func manifestSources(corpus string, sy en16931.Syntax) (map[string]source, error) {
	path := filepath.Join(corpus, string(sy), "manifest.json")
	b, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return map[string]source{}, nil
	}
	if err != nil {
		return nil, err
	}
	var ps []proven
	if err := json.Unmarshal(b, &ps); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	out := make(map[string]source, len(ps))
	for _, pr := range ps {
		if pr.ID == "" || pr.Kind == "" || pr.Kind == "corpus" {
			continue
		}
		out[pr.ID] = source{Kind: pr.Kind, What: pr.What}
	}
	return out, nil
}

// replayMutant validates one corpus file and compares the findings with the
// rule ids its name claims.
func replayMutant(path string, sy en16931.Syntax, man map[string]source, u *universe, st *corpusStats) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	base := filepath.Base(path)
	rep, err := en16931.Validate(data, en16931.Options{Syntax: sy})
	if err != nil {
		fmt.Fprintf(os.Stderr, "%s: %v\n", base, err)
		return nil
	}
	for _, fi := range rep.Findings {
		if fi.Err != "" {
			st.evalErrs++
		}
	}
	ids := firedIDs(rep)
	fired := setOf(ids)
	for _, id := range ids {
		src, ok := man[id]
		if !ok {
			src = source{Kind: "corpus", What: base}
		}
		u.mark(sy, []string{id}, src)
	}
	st.files++
	for _, id := range expectedIDs(base) {
		st.expect++
		if fired[id] {
			continue
		}
		st.missed++
		if len(st.missList) < 20 {
			st.missList = append(st.missList, fmt.Sprintf("%s %s (%s)", sy, id, base))
		}
	}
	return nil
}

// expectedIDs reads the rule ids a corpus file name claims to fire: the part
// before "--", which harvest wrote when it saved the mutant.
func expectedIDs(base string) []string {
	if i := strings.Index(base, "--"); i > 0 {
		return strings.Split(base[:i], "+")
	}
	return nil
}

// basesOf returns the clean reference documents to mutate: the ones named on
// the command line, or every clean document of the reference checkout.
func basesOf(ref, bases string, limit int) ([]string, error) {
	if bases != "" {
		return strings.Split(bases, ","), nil
	}
	clean := cleanBases(ref, limit)
	if len(clean) == 0 {
		return nil, fmt.Errorf("no clean reference documents found under %s", ref)
	}
	return clean, nil
}

// mutantJobs expands each reference document into its mutants.
func mutantJobs(refs []string, doMutate bool) ([]job, error) {
	var work []job
	for _, base := range refs {
		b, err := os.ReadFile(base)
		if err != nil {
			return nil, err
		}
		sy, err := en16931.Detect(b)
		if err != nil {
			return nil, err
		}
		doc, err := xp.ParseXML(string(b))
		if err != nil {
			return nil, err
		}
		if !doMutate {
			continue
		}
		ms := mutate(b, doc.RootElem().Name)
		for i, m := range ms {
			work = append(work, job{base: base, sy: sy, m: m, idx: i})
		}
	}
	return work, nil
}

// validateAll feeds every mutant to handle as the workers finish it, so a
// sweep of hundreds of thousands of mutants never holds them all in memory.
func validateAll(work []job, n int, handle func(o outcome)) {
	in := make(chan job)
	out := make(chan outcome, 64)
	var wg sync.WaitGroup
	for range n {
		wg.Go(func() {
			for j := range in {
				res, err := en16931.Validate(j.m.data, en16931.Options{Syntax: j.sy})
				out <- outcome{j, res, err}
			}
		})
	}
	go func() {
		for _, j := range work {
			in <- j
		}
		close(in)
		wg.Wait()
		close(out)
	}()
	for o := range out {
		handle(o)
	}
}

// workers maps -jobs onto a worker count.
func workers(n int) int {
	if n < 1 {
		return 32
	}
	return n
}

// firing is what the sweep has to remember about a mutant that fired: enough
// to re-derive its bytes later without holding hundreds of thousands of them
// in memory.
type firing struct {
	sy   en16931.Syntax
	base string
	name string
	idx  int
	ids  []string
}

// harvest runs the mutation sweep, recording what each mutant proves and
// saving the ones that prove something new.
//
// Marking and saving happen after the sweep, in mutant order rather than in the
// order the workers finished: thousands of mutants prove overlapping sets of
// asserts, so deciding on the fly would let scheduling pick which of several
// equivalent mutants enters the corpus, and every run would rewrite a different
// set of test files.
func harvest(work []job, n int, corpus string, u *universe) mutantStats {
	st := mutantStats{generated: len(work), why: map[string]int{}, example: map[string]string{}}
	var fired []firing
	validateAll(work, n, func(o outcome) {
		if o.err != nil {
			st.noteRejection(o)
			return
		}
		ids := firedIDs(o.res)
		if len(ids) == 0 {
			return
		}
		st.hits++
		fired = append(fired, firing{sy: o.job.sy, base: o.job.base, name: o.job.m.name, idx: o.job.idx, ids: ids})
	})
	slices.SortFunc(fired, func(a, b firing) int {
		if c := cmp.Compare(a.sy, b.sy); c != 0 {
			return c
		}
		if c := strings.Compare(a.base, b.base); c != 0 {
			return c
		}
		// Position, not name: mutant names come from element local names,
		// which repeat, so names alone are not a total order.
		return cmp.Compare(a.idx, b.idx)
	})
	for _, f := range fired {
		what := filepath.Base(f.base) + " " + f.name
		if u.mark(f.sy, f.ids, source{Kind: "mutant", What: what}) == 0 {
			continue
		}
		m, err := mutantAt(f.base, f.idx)
		if err != nil {
			fail(err)
		}
		if err := saveMutant(corpus, job{base: f.base, sy: f.sy, m: m}, f.ids); err != nil {
			fail(err)
		}
	}
	return st
}

// mutantAt regenerates one mutant of base by its position in mutate's output.
// Mutants are cheap to produce and expensive to keep, so harvest re-derives
// the few hundred it saves instead of holding all of them.
func mutantAt(base string, idx int) (mutant, error) {
	b, err := os.ReadFile(base)
	if err != nil {
		return mutant{}, err
	}
	doc, err := xp.ParseXML(string(b))
	if err != nil {
		return mutant{}, err
	}
	ms := mutate(b, doc.RootElem().Name)
	if idx >= len(ms) {
		return mutant{}, fmt.Errorf("%s: mutant %d of %d is no longer generated", base, idx, len(ms))
	}
	return ms[idx], nil
}

// noteRejection groups mutants the validator could not parse by the reason it
// gave, so one broken edit form shows up as a count rather than as thousands of
// lines.
func (st *mutantStats) noteRejection(o outcome) {
	st.rejected++
	k := o.err.Error()
	if len(k) > 90 {
		k = k[:90]
	}
	st.why[k]++
	ex := o.job.base + " " + o.job.m.name
	if cur, ok := st.example[k]; !ok || ex < cur {
		st.example[k] = ex
	}
}

// saveMutant writes a mutant under the rule ids it fired, which is the
// convention replayCorpus reads back.
func saveMutant(corpus string, j job, ids []string) error {
	dir := filepath.Join(corpus, string(j.sy))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	fn := fmt.Sprintf("%s--%s.xml", sanitize(strings.Join(ids, "+")), sanitize(j.m.name))
	return os.WriteFile(filepath.Join(dir, fn), j.m.data, 0o644)
}

// printRun writes the console report: what each pass proved, and what nothing
// ever fired.
func printRun(u *universe, cov coverage, rs runStats) {
	fmt.Printf("\nofficial test-set payloads: %d, must-fire expectations: %d, not fired: %d\n",
		rs.ts.cases, rs.ts.expect, rs.ts.missed)
	for _, l := range rs.ts.missList {
		fmt.Println("  missing:", l)
	}
	if rs.ts.missed > len(rs.ts.missList) {
		fmt.Printf("  ... %d more\n", rs.ts.missed-len(rs.ts.missList))
	}
	fmt.Printf("mutants: %d generated, %d produced findings, %d rejected\n", rs.ms.generated, rs.ms.hits, rs.ms.rejected)
	printRejections(rs.ms)
	for _, sy := range syntaxNames {
		if st, ok := rs.tg[sy]; ok {
			fmt.Printf("targeted %s: %d candidate asserts, %d insertion mutants, %d still silent\n",
				sy, st.rules, st.made, st.nofire)
		}
	}
	fmt.Println()
	for _, sy := range syntaxNames {
		fmt.Printf("%s: %d/%d active asserts proven (%s)\n",
			sy, len(u.covered[sy]), cov.totals[sy], srcString(cov.bySrc[sy]))
		for _, id := range cov.missing[sy] {
			fmt.Printf("  never fired: %-12s [%s] %s / %s\n",
				id, u.asserts[sy][id], u.pattern[sy][id], strings.Join(u.phases[sy][id], ","))
		}
	}
	printPerPattern(u, cov)
}

// printRejections lists the commonest reasons a mutant could not be read.
func printRejections(ms mutantStats) {
	var ks []string
	for k := range ms.why {
		ks = append(ks, k)
	}
	sort.Slice(ks, func(i, j int) bool { return ms.why[ks[i]] > ms.why[ks[j]] })
	for i, k := range ks {
		if i >= 12 {
			break
		}
		fmt.Printf("  %6d  %s   e.g. %s\n", ms.why[k], k, ms.example[k])
	}
}

// printPerPattern shows coverage pattern by pattern, which is where a gap in
// one part of the model becomes visible.
func printPerPattern(u *universe, cov coverage) {
	for _, sy := range syntaxNames {
		fmt.Printf("\n%s per pattern:\n", sy)
		var pids []string
		for pid := range u.patCount[sy] {
			pids = append(pids, pid)
		}
		sort.Strings(pids)
		for _, pid := range pids {
			fmt.Printf("  %-22s %-22s %d/%d\n", pid, phaseOf(u.phases[sy], u.pattern[sy], pid),
				cov.hits[sy][pid], u.patCount[sy][pid])
		}
	}
}

// proven records how one assert was proven, which is what a replay test needs
// to be able to prove it again.
type proven struct {
	ID   string `json:"id"`
	Kind string `json:"kind"`
	What string `json:"what"`
}

// writeManifests records how every proven assert was proven.
func writeManifests(corpus string, u *universe) error {
	for sy, m := range u.covered {
		var ps []proven
		for id, s := range m {
			ps = append(ps, proven{ID: id, Kind: s.Kind, What: s.What})
		}
		sort.Slice(ps, func(i, j int) bool { return ps[i].ID < ps[j].ID })
		b, err := json.MarshalIndent(ps, "", "  ")
		if err != nil {
			return err
		}
		dir := filepath.Join(corpus, sy)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(dir, "manifest.json"), b, 0o644); err != nil {
			return err
		}
	}
	return nil
}

// syntaxSummary is one syntax's row in the machine-readable summary. The
// run-wide counts repeat per row so a consumer can read one row alone.
type syntaxSummary struct {
	Syntax      string         `json:"syntax"`
	Active      int            `json:"activeAsserts"`
	Proven      int            `json:"provenAsserts"`
	BySource    map[string]int `json:"bySource"`
	NeverFired  []string       `json:"neverFired"`
	Phases      []string       `json:"phases"`
	TestCases   int            `json:"testSetPayloads"`
	MustFire    int            `json:"mustFireExpectations"`
	MustFireOff int            `json:"mustFireNotFired"`
	Mutants     int            `json:"mutants"`
	MutantHits  int            `json:"mutantsWithFindings"`
	Corpus      int            `json:"corpusMutants"`
	CorpusExp   int            `json:"corpusExpectedFirings"`
	CorpusOff   int            `json:"corpusNotFired"`
	EvalErrs    int            `json:"corpusEvalErrors"`
	TargetRules int            `json:"targetedCandidates"`
	TargetMade  int            `json:"targetedMutants"`
}

func writeJSONSummary(path string, u *universe, cov coverage, rs runStats) error {
	sums := []syntaxSummary{}
	for _, sy := range syntaxNames {
		sums = append(sums, syntaxSummary{
			Syntax: sy, Active: cov.totals[sy], Proven: len(u.covered[sy]),
			BySource: cov.bySrc[sy], NeverFired: cov.missing[sy], Phases: u.phaseIDs[sy],
			TestCases: rs.ts.cases, MustFire: rs.ts.expect, MustFireOff: rs.ts.missed,
			Mutants: rs.ms.generated, MutantHits: rs.ms.hits,
			Corpus: rs.cr.files, CorpusExp: rs.cr.expect, CorpusOff: rs.cr.missed,
			EvalErrs:    rs.cr.evalErrs,
			TargetRules: rs.tg[sy].rules, TargetMade: rs.tg[sy].made,
		})
	}
	b, err := json.MarshalIndent(sums, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, b, 0o644)
}

func writeMarkdown(path string, u *universe, cov coverage, rs runStats) error {
	var sb strings.Builder
	sb.WriteString("# Assert coverage\n\n")
	fmt.Fprintf(&sb, "Engine: `%s`\n\n", versionString())
	fmt.Fprintf(&sb, "- official test-set payloads run: %d (%d must-fire expectations, %d of them did not fire)\n",
		rs.ts.cases, rs.ts.expect, rs.ts.missed)
	fmt.Fprintf(&sb, "- corpus replay: %d harvested mutants, %d expected firings, %d did not fire, %d eval errors\n",
		rs.cr.files, rs.cr.expect, rs.cr.missed, rs.cr.evalErrs)
	fmt.Fprintf(&sb, "- clean reference documents: %d\n", rs.refs)
	fmt.Fprintf(&sb, "- mutants generated: %d, producing findings: %d, unparsable: %d\n",
		rs.ms.generated, rs.ms.hits, rs.ms.rejected)
	for _, sy := range syntaxNames {
		if st, ok := rs.tg[sy]; ok {
			fmt.Fprintf(&sb, "- targeted %s: %d candidate asserts, %d insertion mutants, %d still silent\n",
				sy, st.rules, st.made, st.nofire)
		}
	}
	sb.WriteString("\n")
	for _, sy := range syntaxNames {
		fmt.Fprintf(&sb, "## %s: %d/%d active asserts proven\n\n", sy, len(u.covered[sy]), cov.totals[sy])
		fmt.Fprintf(&sb, "sources: %s\n\nphases run by default: %s\n\n",
			srcString(cov.bySrc[sy]), strings.Join(u.phaseIDs[sy], ", "))
		fmt.Fprintf(&sb, "never fired (%d): %s\n\n", len(cov.missing[sy]), strings.Join(cov.missing[sy], ", "))
	}
	return os.WriteFile(path, []byte(sb.String()), 0o644)
}

func main() {
	corpus := flag.String("corpus", "testdata/mutations", "where to write harvested mutants")
	report := flag.String("report", "", "markdown report to write")
	jsonOut := flag.String("json", "", "machine readable summary to write")
	bases := flag.String("bases", "", "comma separated reference documents (default: all clean docs in the corpus)")
	ref := flag.String("ref", envOr("EINVOICE_REF", "../ref-en16931"), "CEF eInvoicing-EN16931 checkout")
	limit := flag.Int("limit", 0, "max bases per syntax (0 = no limit)")
	nomut := flag.Bool("no-mutate", false, "skip the mutation sweep")
	targ := flag.Bool("targeted", false, "build insertion mutants for asserts nothing else ever fired")
	jobs := flag.Int("jobs", 0, "parallel validations (default GOMAXPROCS)")
	flag.Parse()

	u := newUniverse()
	for _, sy := range []en16931.Syntax{en16931.SyntaxCII, en16931.SyntaxUBL} {
		if err := u.loadSyntax(sy); err != nil {
			fail(err)
		}
	}

	ts, err := runTestSets(*ref, u)
	if err != nil {
		fail(err)
	}
	fmt.Fprintf(os.Stderr, "official test sets: %d payload documents, %d must-fire expectations\n", ts.cases, ts.expect)

	cr, err := replayCorpus(*corpus, u)
	if err != nil {
		fail(err)
	}
	fmt.Printf("corpus replay: %d mutants, %d expected firings, %d did not fire, %d could not be evaluated\n",
		cr.files, cr.expect, cr.missed, cr.evalErrs)
	for _, l := range cr.missList {
		fmt.Println("  not fired:", l)
	}

	refs, err := basesOf(*ref, *bases, *limit)
	if err != nil {
		fail(err)
	}
	work, err := mutantJobs(refs, !*nomut)
	if err != nil {
		fail(err)
	}
	fmt.Fprintf(os.Stderr, "%d clean bases, %d mutants\n", len(refs), len(work))

	rs := runStats{ts: ts, cr: cr, ms: harvest(work, workers(*jobs), *corpus, u), refs: len(refs)}
	cov := u.coverage()

	// The targeted pass runs last: it only has something to do for asserts the
	// test sets, the corpus and the sweep all failed to prove.
	if *targ {
		rs.tg = map[string]targetStats{}
		for _, sy := range []en16931.Syntax{en16931.SyntaxCII, en16931.SyntaxUBL} {
			doc, err := targetedBase(refs, sy)
			if err != nil {
				fail(err)
			}
			st, err := targeted(sy, doc, setOf(cov.missing[string(sy)]), workers(*jobs), *corpus, u)
			if err != nil {
				fail(err)
			}
			rs.tg[string(sy)] = st
		}
		cov = u.coverage()
	}
	printRun(u, cov, rs)

	if err := writeManifests(*corpus, u); err != nil {
		fail(err)
	}
	if *jsonOut != "" {
		if err := writeJSONSummary(*jsonOut, u, cov, rs); err != nil {
			fail(err)
		}
	}
	if *report != "" {
		if err := writeMarkdown(*report, u, cov, rs); err != nil {
			fail(err)
		}
	}
}

func phaseOf(ph map[string][]string, pat map[string]string, pid string) string {
	for id, p := range pat {
		if p == pid {
			return strings.Join(ph[id], ",")
		}
	}
	return "-"
}

func srcString(m map[string]int) string {
	var ks []string
	for k := range m {
		ks = append(ks, k)
	}
	sort.Strings(ks)
	var parts []string
	for _, k := range ks {
		parts = append(parts, fmt.Sprintf("%s=%d", k, m[k]))
	}
	return strings.Join(parts, ", ")
}

func versionString() string {
	if v, err := os.ReadFile("VERSION"); err == nil {
		return strings.TrimSpace(string(v))
	}
	return "dev"
}

// cleanBases finds documents in the CEF repository that validate without any
// finding: they are the correct starting points for mutation, because every
// finding on a mutant is then caused by the mutation.
func cleanBases(refDir string, limit int) []string {
	dirs := []string{
		filepath.Join(refDir, "test", "testfiles"),
		filepath.Join(refDir, "cii", "examples"),
	}
	var out []string
	per := map[string]int{}
	for _, d := range dirs {
		files, _ := filepath.Glob(filepath.Join(d, "*.xml"))
		sort.Strings(files)
		for _, f := range files {
			b, err := os.ReadFile(f)
			if err != nil {
				continue
			}
			sy, err := en16931.Detect(b)
			if err != nil {
				continue
			}
			if limit > 0 && per[string(sy)] >= limit {
				continue
			}
			rep, err := en16931.Validate(b, en16931.Options{})
			if err != nil || len(rep.Findings) > 0 {
				continue
			}
			per[string(sy)]++
			out = append(out, f)
		}
	}
	return out
}

func envOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func sanitize(s string) string {
	return strings.NewReplacer("/", "_", " ", "_", "<", "", ">", "", "#", "").Replace(s)
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "cov:", err)
	os.Exit(1)
}
