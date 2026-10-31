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
	"encoding/json"
	"encoding/xml"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
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
			for i := len(stack) - 1; i >= 0; i-- {
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
			aa := a
			add(s.name+"/attr-"+aa.name+"/junk", func(sb *strings.Builder) {
				sb.Write(b[:aa.valueStart])
				sb.WriteString("zz")
				sb.Write(b[aa.valueEnd:])
			})
			add(s.name+"/attr-"+aa.name+"/del", func(sb *strings.Builder) {
				start := aa.tagStart
				for start > s.tagStart && b[start-1] != ' ' && b[start-1] != '\n' && b[start-1] != '\t' && b[start-1] != '\r' {
					start--
				}
				sb.Write(b[:start])
				sb.Write(b[aa.tagEnd:])
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

func main() {
	corpus := flag.String("corpus", "testdata/mutations", "where to write harvested mutants")
	report := flag.String("report", "", "markdown report to write")
	jsonOut := flag.String("json", "", "machine readable summary to write")
	bases := flag.String("bases", "", "comma separated reference documents (default: all clean docs in the corpus)")
	ref := flag.String("ref", envOr("EINVOICE_REF", "../ref-en16931"), "CEF eInvoicing-EN16931 checkout")
	limit := flag.Int("limit", 0, "max bases per syntax (0 = no limit)")
	nomut := flag.Bool("no-mutate", false, "skip the mutation sweep")
	jobs := flag.Int("jobs", 0, "parallel validations (default GOMAXPROCS)")
	flag.Parse()

	// ---- assert universe: only patterns reachable from a phase ever run ----
	asserts := map[string]map[string]string{}  // syntax -> id -> flag
	pattern := map[string]map[string]string{}  // syntax -> id -> pattern id
	phases := map[string]map[string][]string{} // syntax -> id -> phases
	patCount := map[string]map[string]int{}    // syntax -> pattern -> asserts
	var phaseIDs = map[string][]string{}
	for _, sy := range []en16931.Syntax{en16931.SyntaxCII, en16931.SyntaxUBL} {
		s, err := en16931.Schematron(sy)
		if err != nil {
			fail(err)
		}
		phaseIDs[string(sy)] = s.PhaseIDs()
		asserts[string(sy)] = map[string]string{}
		pattern[string(sy)] = map[string]string{}
		phases[string(sy)] = map[string][]string{}
		patCount[string(sy)] = map[string]int{}
		byID := map[string]*sch.Pattern{}
		for _, pat := range s.Patterns {
			byID[pat.ID] = pat
		}
		// Default validation runs with no phase selected, which per ISO
		// Schematron means every pattern is active - including patterns no
		// <phase> references (the UBL schema keeps its UBL-SR-* structural
		// rules in such a pattern).
		refPattern := map[string]bool{}
		for _, ph := range s.Phases {
			for _, pid := range ph.Patterns {
				refPattern[pid] = true
				pat := byID[pid]
				if pat == nil {
					continue
				}
				for _, r := range pat.Rules {
					for _, a := range r.Asserts {
						seen := false
						for _, have := range phases[string(sy)][a.ID] {
							if have == ph.ID {
								seen = true
							}
						}
						if !seen {
							phases[string(sy)][a.ID] = append(phases[string(sy)][a.ID], ph.ID)
						}
					}
				}
			}
		}
		for _, pat := range s.Patterns {
			if !refPattern[pat.ID] {
				n := 0
				for _, r := range pat.Rules {
					n += len(r.Asserts)
				}
				fmt.Fprintf(os.Stderr, "%s: pattern %s (%d asserts) is referenced by no phase but runs by default\n", sy, pat.ID, n)
			}
			for _, r := range pat.Rules {
				for _, a := range r.Asserts {
					asserts[string(sy)][a.ID] = a.Flag
					pattern[string(sy)][a.ID] = pat.ID
					patCount[string(sy)][pat.ID]++
				}
			}
		}
	}

	covered := map[string]map[string]source{} // syntax -> id -> first proven source
	for sy := range asserts {
		covered[sy] = map[string]source{}
	}
	mark := func(sy en16931.Syntax, ids []string, src source) int {
		var n int
		for _, id := range ids {
			if _, ok := covered[string(sy)][id]; !ok {
				covered[string(sy)][id] = src
				n++
			}
		}
		return n
	}
	idsOf := func(rep *en16931.Report) []string {
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

	// ---- pass 1: the official unit test sets ----
	var pts []payload
	if dirs := []string{
		filepath.Join(*ref, "test", "Invoice-unit-UBL"),
		filepath.Join(*ref, "test", "CreditNote-unit-UBL"),
		filepath.Join(*ref, "test", "cii"),
	}; true {
		var err error
		if pts, err = testSets(dirs); err != nil {
			fail(err)
		}
	}
	var tsCases, tsExpect, tsMissed int
	var tsMissList []string
	for _, p := range pts {
		rep, err := en16931.ValidateDoc(p.sy, p.doc, nil)
		if err != nil {
			fmt.Fprintf(os.Stderr, "%s: %v\n", p.file, err)
			continue
		}
		tsCases++
		fired := map[string]bool{}
		for _, id := range idsOf(rep) {
			fired[id] = true
		}
		mark(p.sy, idsOf(rep), source{Kind: "official-testset", What: filepath.Base(p.file) + "#" + fmt.Sprint(p.idx)})
		for _, id := range p.mustFire {
			tsExpect++
			if !fired[id] {
				tsMissed++
				if len(tsMissList) < 40 {
					tsMissList = append(tsMissList, fmt.Sprintf("%s %s (%s#%d)", p.sy, id, filepath.Base(p.file), p.idx))
				}
			}
		}
	}
	fmt.Fprintf(os.Stderr, "official test sets: %d payload documents, %d must-fire expectations\n", tsCases, tsExpect)

	// ---- pass 1b: replay the corpus harvested by an earlier run ----
	// The rules each harvested mutant is expected to fire are encoded in its
	// file name, so this pass needs no bookkeeping: it re-checks that the
	// corpus still proves what it proved when it was harvested, and it lets
	// -no-mutate report full coverage in a couple of seconds.
	var cf, ce, cm, cerr int
	var cmList []string
	for _, sy := range []en16931.Syntax{en16931.SyntaxCII, en16931.SyntaxUBL} {
		fs, err := filepath.Glob(filepath.Join(*corpus, string(sy), "*.xml"))
		if err != nil {
			fail(err)
		}
		sort.Strings(fs)
		for _, f := range fs {
			data, err := os.ReadFile(f)
			if err != nil {
				fail(err)
			}
			base := filepath.Base(f)
			var ids []string
			if i := strings.Index(base, "--"); i > 0 {
				ids = strings.Split(base[:i], "+")
			}
			rep, err := en16931.Validate(data, en16931.Options{Syntax: sy})
			if err != nil {
				fmt.Fprintf(os.Stderr, "%s: %v\n", base, err)
				continue
			}
			for _, fi := range rep.Findings {
				if fi.Err != "" {
					cerr++
				}
			}
			fired := map[string]bool{}
			for _, id := range idsOf(rep) {
				fired[id] = true
			}
			mark(sy, idsOf(rep), source{Kind: "corpus", What: base})
			cf++
			for _, id := range ids {
				ce++
				if !fired[id] {
					cm++
					if len(cmList) < 20 {
						cmList = append(cmList, fmt.Sprintf("%s %s (%s)", sy, id, base))
					}
				}
			}
		}
	}
	fmt.Printf("corpus replay: %d mutants, %d expected firings, %d did not fire, %d could not be evaluated\n", cf, ce, cm, cerr)
	for _, l := range cmList {
		fmt.Println("  not fired:", l)
	}

	// ---- pass 2: mutants of clean reference documents ----
	var refs []string
	if *bases != "" {
		refs = strings.Split(*bases, ",")
	} else {
		refs = cleanBases(*ref, *limit)
	}
	if len(refs) == 0 {
		fail(fmt.Errorf("no clean reference documents found under %s", *ref))
	}
	var work []job
	for _, base := range refs {
		b, err := os.ReadFile(base)
		if err != nil {
			fail(err)
		}
		sy, err := en16931.Detect(b)
		if err != nil {
			fail(err)
		}
		doc, err := xp.ParseXML(string(b))
		if err != nil {
			fail(err)
		}
		if !*nomut {
			for _, m := range mutate(b, doc.RootElem().Name) {
				work = append(work, job{base: base, sy: sy, m: m})
			}
		}
	}
	fmt.Fprintf(os.Stderr, "%d clean bases, %d mutants\n", len(refs), len(work))

	n := *jobs
	if n < 1 {
		n = 32
	}
	in := make(chan job)
	out := make(chan outcome, 64)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := range in {
				res, err := en16931.Validate(j.m.data, en16931.Options{Syntax: j.sy})
				out <- outcome{j, res, err}
			}
		}()
	}
	go func() {
		for _, j := range work {
			in <- j
		}
		close(in)
		wg.Wait()
		close(out)
	}()

	dead, firedMut := 0, 0
	deadWhy := map[string]int{}
	deadEx := map[string]string{}
	for o := range out {
		if o.err != nil {
			dead++
			k := o.err.Error()
			if len(k) > 90 {
				k = k[:90]
			}
			deadWhy[k]++
			if _, ok := deadEx[k]; !ok {
				deadEx[k] = o.job.base + " " + o.job.m.name
			}
			continue
		}
		nf := idsOf(o.res)
		if len(nf) == 0 {
			continue
		}
		firedMut++
		if mark(o.job.sy, nf, source{Kind: "mutant", What: filepath.Base(o.job.base) + " " + o.job.m.name}) > 0 {
			dir := filepath.Join(*corpus, string(o.job.sy))
			if err := os.MkdirAll(dir, 0o755); err != nil {
				fail(err)
			}
			fn := fmt.Sprintf("%s--%s.xml", sanitize(strings.Join(nf, "+")), sanitize(o.job.m.name))
			if err := os.WriteFile(filepath.Join(dir, fn), o.job.m.data, 0o644); err != nil {
				fail(err)
			}
		}
	}

	// ---- report ----
	totals := map[string]int{}
	missing := map[string][]string{}
	for sy, m := range asserts {
		totals[sy] = len(m)
		for id := range m {
			if _, ok := covered[sy][id]; !ok {
				missing[sy] = append(missing[sy], id)
			}
		}
		sort.Strings(missing[sy])
	}
	fmt.Printf("\nofficial test-set payloads: %d, must-fire expectations: %d, not fired: %d\n", tsCases, tsExpect, tsMissed)
	for _, l := range tsMissList {
		fmt.Println("  missing:", l)
	}
	if tsMissed > len(tsMissList) {
		fmt.Printf("  ... %d more\n", tsMissed-len(tsMissList))
	}
	fmt.Printf("mutants: %d generated, %d produced findings, %d rejected\n", len(work), firedMut, dead)
	var ks []string
	for k := range deadWhy {
		ks = append(ks, k)
	}
	sort.Slice(ks, func(i, j int) bool { return deadWhy[ks[i]] > deadWhy[ks[j]] })
	for i, k := range ks {
		if i >= 12 {
			break
		}
		fmt.Printf("  %6d  %s   e.g. %s\n", deadWhy[k], k, deadEx[k])
	}
	fmt.Println()

	bySrc := map[string]map[string]int{}
	for sy, m := range covered {
		bySrc[sy] = map[string]int{}
		for _, s := range m {
			bySrc[sy][s.Kind]++
		}
	}
	for _, sy := range []string{"CII", "UBL"} {
		fmt.Printf("%s: %d/%d active asserts proven (%s)\n", sy, len(covered[sy]), totals[sy], srcString(bySrc[sy]))
		for _, id := range missing[sy] {
			fmt.Printf("  never fired: %-12s [%s] %s / %s\n", id, asserts[sy][id], pattern[sy][id], strings.Join(phases[sy][id], ","))
		}
	}
	for _, sy := range []string{"CII", "UBL"} {
		fmt.Printf("\n%s per pattern:\n", sy)
		var pids []string
		for pid := range patCount[sy] {
			pids = append(pids, pid)
		}
		sort.Strings(pids)
		for _, pid := range pids {
			var hit int
			for id, p := range pattern[sy] {
				if p == pid {
					if _, ok := covered[sy][id]; ok {
						hit++
					}
				}
			}
			fmt.Printf("  %-22s %-22s %d/%d\n", pid, phaseOf(phases[sy], pattern[sy], pid), hit, patCount[sy][pid])
		}
	}

	// manifest: how each assert was proven
	type proven struct {
		ID   string `json:"id"`
		Kind string `json:"kind"`
		What string `json:"what"`
	}
	for sy, m := range covered {
		var ps []proven
		for id, s := range m {
			ps = append(ps, proven{ID: id, Kind: s.Kind, What: s.What})
		}
		sort.Slice(ps, func(i, j int) bool { return ps[i].ID < ps[j].ID })
		b, _ := json.MarshalIndent(ps, "", "  ")
		dir := filepath.Join(*corpus, sy)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			fail(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "manifest.json"), b, 0o644); err != nil {
			fail(err)
		}
	}

	if *jsonOut != "" {
		type sum struct {
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
		}
		var sums []sum
		for _, sy := range []string{"CII", "UBL"} {
			sums = append(sums, sum{Syntax: sy, Active: totals[sy], Proven: len(covered[sy]),
				BySource: bySrc[sy], NeverFired: missing[sy], Phases: phaseIDs[sy],
				TestCases: tsCases, MustFire: tsExpect, MustFireOff: tsMissed,
				Mutants: len(work), MutantHits: firedMut,
				Corpus: cf, CorpusExp: ce, CorpusOff: cm, EvalErrs: cerr})
		}
		b, _ := json.MarshalIndent(sums, "", "  ")
		if err := os.WriteFile(*jsonOut, b, 0o644); err != nil {
			fail(err)
		}
	}

	if *report != "" {
		var sb strings.Builder
		sb.WriteString("# Assert coverage\n\n")
		fmt.Fprintf(&sb, "Engine: `%s`\n\n", versionString())
		fmt.Fprintf(&sb, "- official test-set payloads run: %d (%d must-fire expectations, %d of them did not fire)\n", tsCases, tsExpect, tsMissed)
		fmt.Fprintf(&sb, "- corpus replay: %d harvested mutants, %d expected firings, %d did not fire, %d eval errors\n", cf, ce, cm, cerr)
		fmt.Fprintf(&sb, "- clean reference documents: %d\n", len(refs))
		fmt.Fprintf(&sb, "- mutants generated: %d, producing findings: %d, unparsable: %d\n\n", len(work), firedMut, dead)
		for _, sy := range []string{"CII", "UBL"} {
			fmt.Fprintf(&sb, "## %s: %d/%d active asserts proven\n\n", sy, len(covered[sy]), totals[sy])
			fmt.Fprintf(&sb, "sources: %s\n\nphases run by default: %s\n\n", srcString(bySrc[sy]), strings.Join(phaseIDs[sy], ", "))
			fmt.Fprintf(&sb, "never fired (%d): %s\n\n", len(missing[sy]), strings.Join(missing[sy], ", "))
		}
		if err := os.WriteFile(*report, []byte(sb.String()), 0o644); err != nil {
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
