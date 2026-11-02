package main

import (
	"bytes"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/dcnws/invoicekit.go/internal/en16931"
)

// tdoc is a small document exercising the byte level path walking: a comment
// before the first child, a prefixed root, a nested element of the same name
// and a self-closing child.
const tdoc = `<?xml version="1.0"?>
<rsm:Doc xmlns:rsm="urn:rsm" xmlns:ram="urn:ram">
  <!-- a comment -->
  <ram:Period>
    <ram:A>1</ram:A>
    <ram:B><ram:C><ram:D>v</ram:D></ram:C></ram:B>
    <ram:Self/>
  </ram:Period>
</rsm:Doc>`

func TestDocPrefixes(t *testing.T) {
	got := docPrefixes([]byte(tdoc))
	if got["urn:rsm"] != "rsm" || got["urn:ram"] != "ram" {
		t.Fatalf("prefixes: %v", got)
	}
	if len(got) != 2 {
		t.Fatalf("want two namespaces, got %v", got)
	}
}

func TestXlateQName(t *testing.T) {
	smap := map[string]string{"ram": "urn:ram", "cac": "urn:cac"}
	dmap := map[string]string{"urn:ram": "ram"}
	for _, tc := range []struct{ in, want string }{
		{"ram:Period", "ram:Period"},
		{"cac:Period", "cac:Period"}, // no prefix in the document: leave it alone
		{"Period", "Period"},
	} {
		if got := xlateQName(tc.in, smap, dmap); got != tc.want {
			t.Errorf("xlateQName(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestParsePath(t *testing.T) {
	desc, steps, ok := parsePath("//ram:BillingSpecifiedPeriod")
	if !ok || !desc || len(steps) != 1 || steps[0].name != "ram:BillingSpecifiedPeriod" {
		t.Fatalf("// path: %v %v %v", desc, steps, ok)
	}
	desc, steps, ok = parsePath("ram:A/ram:B[ram:C='v'][ram:D]")
	if !ok || desc || len(steps) != 2 {
		t.Fatalf("child path: %v %v %v", desc, steps, ok)
	}
	if steps[1].name != "ram:B" || steps[1].preds != "[ram:C='v'][ram:D]" {
		t.Fatalf("predicates kept as %q on %q", steps[1].preds, steps[1].name)
	}
	for _, bad := range []string{"ram:A/@ram:B", "text()", "ram:A/*", ""} {
		if _, _, ok := parsePath(bad); ok {
			t.Errorf("%q parsed as an element path", bad)
		}
	}
}

func TestExtentAndChildren(t *testing.T) {
	b := []byte(tdoc)
	lo, hi, ok := extent(b, "ram:Period", 0)
	if !ok {
		t.Fatal("ram:Period not found")
	}
	if !bytesContains(b[lo:hi], "<ram:Self/>") || !strings.HasSuffix(strings.TrimSpace(string(b[lo:hi])), "</ram:Period>") {
		t.Fatalf("extent [%d,%d) is not the whole element: %q", lo, hi, string(b[lo:hi]))
	}
	kids := children(b, lo, hi)
	var names []string
	for _, c := range kids {
		names = append(names, c.name)
	}
	if !slices.Equal(names, []string{"ram:A", "ram:B", "ram:Self"}) {
		t.Fatalf("children: %v", names)
	}
	// The comment between the parent's start tag and its first child must not
	// end the walk, and each child must span exactly itself.
	for _, c := range kids {
		if c.lo < lo || c.hi > hi {
			t.Fatalf("child %s escapes its parent", c.name)
		}
	}
	nested := []byte(`<r><a>1<a>2</a>3</a></r>`)
	lo, hi, ok = extent(nested, "a", 0)
	if !ok || string(nested[lo:hi]) != "<a>1<a>2</a>3</a>" {
		t.Fatalf("nested same name: [%d,%d) ok=%v", lo, hi, ok)
	}
	if _, _, ok := extent(nested, "b", 0); ok {
		t.Fatal("extent found a name that is not there")
	}
}

func TestPredOK(t *testing.T) {
	b := []byte(tdoc)
	lo, hi, _ := extent(b, "ram:Period", 0)
	n := node{name: "ram:Period", lo: lo, hi: hi}
	smap := map[string]string{"ram": "urn:ram"}
	dmap := map[string]string{"urn:ram": "ram"}
	for _, tc := range []struct {
		preds string
		want  bool
	}{
		{"", true},
		{"[ram:A]", true},
		{"[ram:Nope]", false},
		{"[ram:B/ram:C/ram:D='v']", true},
		{"[ram:B/ram:C/ram:D='w']", false},
		{"[ram:B][ram:Self]", true},
		{"[ram:A > 1]", false}, // unsupported predicate: refuse rather than guess
	} {
		if got := predOK(b, n, tc.preds, smap, dmap); got != tc.want {
			t.Errorf("predOK(%q) = %v, want %v", tc.preds, got, tc.want)
		}
	}
}

func TestWalk(t *testing.T) {
	b := []byte(tdoc)
	smap := map[string]string{"ram": "urn:ram", "cac": "urn:cac"}
	dmap := map[string]string{"urn:ram": "ram"}
	n, ok := walk(b, "//ram:Period", 0, len(b), smap, dmap)
	if !ok {
		t.Fatal("//ram:Period not found")
	}
	if !bytesContains(b[n.lo:n.hi], "<ram:A>1</ram:A>") {
		t.Fatalf("descendant walk landed on %q", string(b[n.lo:n.hi]))
	}
	if _, ok := walk(b, "rsm:Doc/ram:Period", 0, len(b), smap, dmap); !ok {
		t.Fatal("child walk failed")
	}
	if _, ok := walk(b, "ram:Period", 0, len(b), smap, dmap); ok {
		t.Fatal("child walk matched a descendant")
	}
	if _, ok := walk(b, "//cac:Period", 0, len(b), smap, dmap); ok {
		t.Fatal("walk matched a namespace the document does not use")
	}
	n, ok = walk(b, "//ram:Period[ram:A]", 0, len(b), smap, dmap)
	if !ok {
		t.Fatal("predicate on a descendant step failed")
	}
	if !bytesContains(b[n.lo:n.hi], "ram:Self") {
		t.Fatalf("predicate landed on the wrong node: %q", string(b[n.lo:n.hi]))
	}
}

func TestFrag(t *testing.T) {
	smap := map[string]string{"ram": "urn:ram", "cac": "urn:cac"}
	dmap := map[string]string{"urn:ram": "ram"}
	got, ok := frag("ram:A/ram:B", smap, dmap)
	if !ok || string(got) != "<ram:A><ram:B>x</ram:B></ram:A>" {
		t.Fatalf("frag = %q ok=%v", got, ok)
	}
	if _, ok := frag("a/b/c/d/e", map[string]string{}, map[string]string{}); ok {
		t.Fatal("frag accepted a chain five elements deep")
	}
}

func TestInsert(t *testing.T) {
	b := []byte(tdoc)
	n, _ := walk(b, "//ram:Period", 0, len(b), nil, nil)
	f := []byte("<ram:New>x</ram:New>")
	got := insert(b, n, f)
	// One variant before each child plus one just before the end tag.
	if len(got) != 4 {
		t.Fatalf("variants: %d", len(got))
	}
	for i, v := range got {
		if !bytesContains(v, "<ram:New>x</ram:New>") {
			t.Fatalf("variant %d has no insertion", i)
		}
	}
	if !bytesContains(got[0], "<ram:New>x</ram:New>\n    <ram:A>") {
		t.Fatalf("variant 0 lost the indentation of the next sibling: %q", firstLines(got[0]))
	}
	// A self-closing parent becomes an element with one child.
	sb := []byte(`<r><ram:Self/></r>`)
	sn, ok := walk(sb, "//ram:Self", 0, len(sb), nil, nil)
	if !ok {
		t.Fatal("self-closing element not found")
	}
	vs := insert(sb, sn, f)
	if len(vs) != 1 || string(vs[0]) != "<r><ram:Self><ram:New>x</ram:New></ram:Self></r>" {
		t.Fatalf("self-closing variants: %q", vs)
	}
}

func TestTargetRules(t *testing.T) {
	s, err := en16931.Schematron(en16931.SyntaxCII)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]bool{"CII-DT-069": true, "BR-01": true}
	var ids []string
	for _, r := range targetRules(s, want) {
		ids = append(ids, r.id)
		if r.id == "CII-DT-069" {
			if r.chain != "ram:DurationMeasure" {
				t.Errorf("chain = %q", r.chain)
			}
			if r.ctx != "//ram:BillingSpecifiedPeriod" {
				t.Errorf("context = %q", r.ctx)
			}
		}
	}
	if !slices.Equal(ids, []string{"CII-DT-069"}) {
		t.Fatalf("candidates: %v", ids)
	}
}

func TestLeaf(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"//ram:BillingSpecifiedPeriod", "BillingSpecifiedPeriod"},
		{"cac:InvoiceLine | //ram:Group[1]", "InvoiceLine"},
		{"ram:A/udt:AmountMeasure", "AmountMeasure"},
	} {
		if got := leaf(tc.in); got != tc.want {
			t.Errorf("leaf(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// TestTargetedHarvest is the end to end proof: the targeted pass must build a
// document that the XML schema accepts and that makes the assert speak.
func TestTargetedHarvest(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "testdata", "samples", "cii-xrechnung.xml"))
	if err != nil {
		t.Fatal(err)
	}
	u := newUniverse()
	if err := u.loadSyntax(en16931.SyntaxCII); err != nil {
		t.Fatal(err)
	}
	corpus := t.TempDir()
	st, err := targeted(en16931.SyntaxCII, data, map[string]bool{"CII-DT-069": true}, 4, corpus, u)
	if err != nil {
		t.Fatal(err)
	}
	if st.rules != 1 || st.made != 1 {
		t.Fatalf("stats: %+v", st)
	}
	files, err := filepath.Glob(filepath.Join(corpus, "CII", "*.xml"))
	if err != nil || len(files) != 1 {
		t.Fatalf("corpus files: %v (%v)", files, err)
	}
	base := filepath.Base(files[0])
	if base != "CII-DT-069--ins-DurationMeasure-under-BillingSpecifiedPeriod.xml" {
		t.Errorf("file name: %s", base)
	}
	if !slices.Contains(expectedIDs(base), "CII-DT-069") {
		t.Errorf("%s does not claim its target rule", base)
	}
	// The saved mutant must be what it claims: schema valid, every rule
	// evaluable, and the target assert firing.
	mut, err := os.ReadFile(files[0])
	if err != nil {
		t.Fatal(err)
	}
	rep, err := en16931.Validate(mut, en16931.Options{Syntax: en16931.SyntaxCII})
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Schema) != 0 || rep.Internal != 0 {
		t.Fatalf("schema findings %d, unevaluated rules %d", len(rep.Schema), rep.Internal)
	}
	if !slices.Contains(firedIDs(rep), "CII-DT-069") {
		t.Errorf("CII-DT-069 did not fire: %v", firedIDs(rep))
	}
	if _, ok := u.covered[string(en16931.SyntaxCII)]["CII-DT-069"]; !ok {
		t.Error("the harvested mutant did not mark the assert proven")
	}
}

// TestFirstFiringOrder pins the choice down: several insertion positions can be
// schema valid and fire the rule, and the pass must always settle on the same
// one, or re-running it would rewrite the corpus it harvested before.
func TestFirstFiringOrder(t *testing.T) {
	doc, err := os.ReadFile(filepath.Join("..", "..", "testdata", "samples", "ubl-peppol.xml"))
	if err != nil {
		t.Fatal(err)
	}
	s, err := en16931.Schematron(en16931.SyntaxUBL)
	if err != nil {
		t.Fatal(err)
	}
	smap, dmap := s.NS, docPrefixes(doc)
	rules := targetRules(s, map[string]bool{"UBL-CR-342": true})
	if len(rules) != 1 {
		t.Fatalf("rules: %v", rules)
	}
	f, ok := frag(rules[0].chain, smap, dmap)
	if !ok {
		t.Fatal("no fragment")
	}
	vs := variantsAt(doc, rules[0].ctx, f, smap, dmap)
	if len(vs) != 18 {
		t.Fatalf("variants: %d", len(vs))
	}
	keep, ids := firstFiring(en16931.SyntaxUBL, "UBL-CR-342", vs, 8)
	if keep == nil {
		t.Fatal("no variant fired UBL-CR-342")
	}
	if !slices.Contains(ids, "UBL-CR-342") {
		t.Errorf("ids: %v", ids)
	}
	if !slices.ContainsFunc(vs, func(v []byte) bool { return bytes.Equal(v, keep) }) {
		t.Error("the kept mutant is not one of the variants")
	}
	for i, v := range vs {
		if bytes.Equal(v, keep) {
			if i != 11 {
				t.Fatalf("picked variant %d, want the earliest valid one (11)", i)
			}
			return
		}
	}
}

func bytesContains(b []byte, s string) bool {
	return strings.Contains(string(b), s)
}

func firstLines(b []byte) string {
	i := strings.Index(string(b), "<ram:New")
	if i < 0 || i > len(b) {
		return string(b)
	}
	lo := max(0, i-40)
	return string(b[lo:min(i+120, len(b))])
}
