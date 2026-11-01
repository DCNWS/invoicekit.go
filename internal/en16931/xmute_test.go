package en16931

import (
	"fmt"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/dcnws/invoicekit.go/internal/sch"
	"github.com/dcnws/invoicekit.go/internal/xp"
)

// The XRechnung rule set ships its own rule tests as complete invoice
// instances that carry the mutation to apply as an XML processing instruction:
//
//	<?xmute mutator="code" attribute="unitCode" values="XPK,XRO"
//	      schematron-invalid="xrcii:BR-DE-14" id="..." description="..."?>
//	<ram:BasisQuantity unitCode="XPP">1.0</ram:BasisQuantity>
//
// The instruction applies to the element that follows it, and one file holds
// many: each instruction is one case of upstream's test matrix. Replaying the
// suite means applying every instruction to a copy of the document and checking
// that the asserted rules speak, or keep silent, exactly as the case says.
//
// Mutations are applied to the source text at byte offsets instead of by
// parsing and re-encoding the XML: encoding/xml renames namespace prefixes and
// repeats default namespace declarations, so a round trip would rewrite every
// document and a broken expectation could no longer be told apart from the
// rewrite itself.

// xmuteMutators are the mutators upstream's generator understands.
var xmuteMutators = map[string]bool{
	"identity":   true, // leave the document as it stands
	"code":       true, // set element text, or one attribute, to a value
	"empty":      true, // clear element text, or one attribute
	"remove":     true, // drop the element, or the attribute
	"whitespace": true, // pad element text with whitespace
}

// xmuteSyntax maps an expectation prefix to the syntax whose rule set the
// asserted rule belongs to.
var xmuteSyntax = map[string]Syntax{"xrcii": SyntaxCII, "xrubl": SyntaxUBL}

// xmuteTarget is the byte span of the element an instruction applies to.
type xmuteTarget struct {
	ok          bool
	name        string
	start       int // '<' of the start tag
	tagEnd      int // just after the '>' or '/>' of the start tag
	innerStart  int // start of the element's content
	innerEnd    int // end of the element's content
	end         int // just after the whole element
	selfClosing bool
}

// xmuteInst is one instruction plus its target.
type xmuteInst struct {
	mutator string
	id      string
	attr    string
	values  []string
	valid   []string
	invalid []string
	target  xmuteTarget
}

func (x xmuteInst) String() string {
	if x.mutator == "code" {
		return fmt.Sprintf("%s %s", x.id, x.values)
	}
	return x.id
}

// namesPeppol reports whether the instruction asserts a Peppol BIS rule. Only
// those cases need the Peppol bundle to run, and running it is the single most
// expensive part of the replay, so the answer is computed once per instruction.
func (x xmuteInst) namesPeppol() bool {
	for _, refs := range [][]string{x.invalid, x.valid} {
		for _, id := range refs {
			if strings.HasPrefix(id, "PEPPOL-") {
				return true
			}
		}
	}
	return false
}

// xmuteAttrRE picks name="value" pairs out of an instruction. The corpus never
// quotes a value that contains a double quote, so a flat pattern is enough.
var xmuteAttrRE = regexp.MustCompile(`([\w-]+)\s*=\s*"([^"]*)"`)

// parseXmute reads one instruction body, given without the surrounding <? ?>.
func parseXmute(pi string) (xmuteInst, error) {
	a := map[string]string{}
	for _, m := range xmuteAttrRE.FindAllStringSubmatch(pi, -1) {
		a[m[1]] = m[2]
	}
	x := xmuteInst{mutator: a["mutator"], id: a["id"], attr: a["attribute"]}
	if x.mutator == "" {
		return xmuteInst{}, fmt.Errorf("instruction has no mutator: %.120s", oneline(pi))
	}
	if !xmuteMutators[x.mutator] {
		return xmuteInst{}, fmt.Errorf("unknown mutator %q", x.mutator)
	}
	if x.id == "" {
		x.id = "<unnamed>"
	}
	sep := a["separator"]
	if sep == "" {
		sep = ","
	}
	switch x.mutator {
	case "code":
		if a["values"] == "" {
			return xmuteInst{}, fmt.Errorf("%s: mutator code carries no values", x.id)
		}
		// Upstream lists one value per run and separates them with separator;
		// surrounding whitespace is not part of a value.
		for v := range strings.SplitSeq(a["values"], sep) {
			x.values = append(x.values, strings.TrimSpace(v))
		}
	default:
		x.values = []string{""}
	}
	var err error
	if x.valid, err = xmuteRefs(a["schematron-valid"]); err != nil {
		return xmuteInst{}, fmt.Errorf("%s: %w", x.id, err)
	}
	if x.invalid, err = xmuteRefs(a["schematron-invalid"]); err != nil {
		return xmuteInst{}, fmt.Errorf("%s: %w", x.id, err)
	}
	if len(x.valid)+len(x.invalid) == 0 {
		return xmuteInst{}, fmt.Errorf("%s: the instruction states no expectation", x.id)
	}
	return x, nil
}

// xmuteRefs splits one expectation attribute. Upstream writes several rule ids
// into the same attribute, separated by spaces or commas, and every id carries
// the prefix of the syntax it belongs to.
func xmuteRefs(raw string) ([]string, error) {
	var out []string
	for _, tok := range strings.FieldsFunc(raw, func(r rune) bool {
		return r == ',' || r == ' ' || r == '\t' || r == '\n' || r == '\r'
	}) {
		prefix, id, found := strings.Cut(tok, ":")
		if !found {
			prefix, id = tok, tok
		}
		if _, ok := xmuteSyntax[prefix]; !ok {
			return nil, fmt.Errorf("unknown rule prefix %q", prefix)
		}
		if id == "" {
			return nil, fmt.Errorf("empty rule id in %q", raw)
		}
		out = append(out, id)
	}
	return out, nil
}

// xTagKind classifies a markup construct.
type xTagKind int

const (
	xTagOther xTagKind = iota // comment, processing instruction, CDATA, declaration
	xTagStart
	xTagEnd
)

// xTag is one markup construct of the source text.
type xTag struct {
	name string
	kind xTagKind
	end  int // index just after the construct
	self bool
}

// xTagAt reads the markup construct that starts at i, where src[i] == '<'.
func xTagAt(src string, i int) (xTag, error) {
	rest := src[i:]
	switch {
	case strings.HasPrefix(rest, "<!--"):
		j := strings.Index(rest, "-->")
		if j < 0 {
			return xTag{}, fmt.Errorf("comment at byte %d is never closed", i)
		}
		return xTag{kind: xTagOther, end: i + j + 3}, nil
	case strings.HasPrefix(rest, "<![CDATA["):
		j := strings.Index(rest, "]]>")
		if j < 0 {
			return xTag{}, fmt.Errorf("CDATA at byte %d is never closed", i)
		}
		return xTag{kind: xTagOther, end: i + j + 3}, nil
	case strings.HasPrefix(rest, "<?"):
		j := strings.Index(rest, "?>")
		if j < 0 {
			return xTag{}, fmt.Errorf("processing instruction at byte %d is never closed", i)
		}
		return xTag{kind: xTagOther, end: i + j + 2}, nil
	case strings.HasPrefix(rest, "</"):
		name, j := xNameAt(rest, 2)
		if name == "" {
			return xTag{}, fmt.Errorf("end tag at byte %d has no name", i)
		}
		gt := strings.IndexByte(rest[j:], '>')
		if gt < 0 {
			return xTag{}, fmt.Errorf("end tag at byte %d is never closed", i)
		}
		return xTag{name: name, kind: xTagEnd, end: i + j + gt + 1}, nil
	case len(rest) > 1 && rest[1] == '!':
		gt := strings.IndexByte(rest, '>')
		if gt < 0 {
			return xTag{}, fmt.Errorf("declaration at byte %d is never closed", i)
		}
		return xTag{kind: xTagOther, end: i + gt + 1}, nil
	}
	name, j := xNameAt(rest, 1)
	if name == "" {
		return xTag{}, fmt.Errorf("markup at byte %d is not a tag", i)
	}
	for k := j; k < len(rest); k++ {
		switch rest[k] {
		case '"', '\'':
			q := strings.IndexByte(rest[k+1:], rest[k])
			if q < 0 {
				return xTag{}, fmt.Errorf("tag at byte %d has an unterminated quote", i)
			}
			k += q + 1
		case '>':
			return xTag{name: name, kind: xTagStart, end: i + k + 1}, nil
		case '/':
			if k+1 < len(rest) && rest[k+1] == '>' {
				return xTag{name: name, kind: xTagStart, end: i + k + 2, self: true}, nil
			}
		}
	}
	return xTag{}, fmt.Errorf("tag at byte %d is never closed", i)
}

// xNameAt reads an XML name starting at off.
func xNameAt(s string, off int) (string, int) {
	k := off
	for k < len(s) && xNameByte(s[k]) {
		k++
	}
	return s[off:k], k
}

func xNameByte(c byte) bool {
	return c == ':' || c == '_' || c == '-' || c == '.' ||
		('a' <= c && c <= 'z') || ('A' <= c && c <= 'Z') ||
		('0' <= c && c <= '9') || c >= 0x80
}

func xSpace(c byte) bool { return c == ' ' || c == '\t' || c == '\n' || c == '\r' }

// xSkipMisc advances past whitespace, comments, processing instructions and
// CDATA: everything that may sit between an instruction and its element.
func xSkipMisc(src string, i int) (int, error) {
	for i < len(src) {
		if src[i] != '<' {
			if xSpace(src[i]) {
				i++
				continue
			}
			return 0, fmt.Errorf("text at byte %d, expected an element", i)
		}
		t, err := xTagAt(src, i)
		if err != nil {
			return 0, err
		}
		if t.kind != xTagOther {
			return i, nil
		}
		i = t.end
	}
	return i, nil
}

// xElementExtent measures the element whose start tag begins at start.
func xElementExtent(src string, start int) (xmuteTarget, error) {
	t0, err := xTagAt(src, start)
	if err != nil {
		return xmuteTarget{}, err
	}
	if t0.kind != xTagStart {
		return xmuteTarget{}, fmt.Errorf("byte %d does not start an element", start)
	}
	t := xmuteTarget{ok: true, name: t0.name, start: start, tagEnd: t0.end, selfClosing: t0.self}
	if t0.self {
		t.end, t.innerStart, t.innerEnd = t0.end, t0.end, t0.end
		return t, nil
	}
	i := t0.end
	for depth := 1; i < len(src); {
		if src[i] != '<' {
			i++
			continue
		}
		tg, err := xTagAt(src, i)
		if err != nil {
			return xmuteTarget{}, err
		}
		switch tg.kind {
		case xTagOther:
		case xTagStart:
			if tg.name == t.name && !tg.self {
				depth++
			}
		case xTagEnd:
			if tg.name == t.name {
				depth--
				if depth == 0 {
					t.end, t.innerStart, t.innerEnd = tg.end, t0.end, i
					return t, nil
				}
			}
		}
		i = tg.end
	}
	return xmuteTarget{}, fmt.Errorf("element %s at byte %d is never closed", t0.name, start)
}

// xMutes finds every mutation instruction of a document, together with the
// element that follows it. An instruction inside a comment is skipped: a
// commented-out instruction is a disabled test upstream, too.
func xMutes(src string) ([]xmuteInst, error) {
	var out []xmuteInst
	for i := 0; i < len(src); {
		if src[i] != '<' {
			i++
			continue
		}
		tg, err := xTagAt(src, i)
		if err != nil {
			return nil, err
		}
		if xIsXMute(src, i) {
			x, err := parseXmute(src[i:tg.end])
			if err != nil {
				return nil, fmt.Errorf("byte %d: %w", i, err)
			}
			j, err := xSkipMisc(src, tg.end)
			if err != nil {
				return nil, fmt.Errorf("%s: %w", x.id, err)
			}
			if j >= len(src) {
				return nil, fmt.Errorf("%s: no element follows the instruction", x.id)
			}
			if x.target, err = xElementExtent(src, j); err != nil {
				return nil, fmt.Errorf("%s: %w", x.id, err)
			}
			out = append(out, x)
		}
		i = tg.end
	}
	return out, nil
}

// xIsXMute reports whether the processing instruction at i is a mutation
// instruction, as opposed to some other processing instruction.
func xIsXMute(src string, i int) bool {
	const p = "<?xmute"
	if !strings.HasPrefix(src[i:], p) || i+len(p) >= len(src) {
		return false
	}
	return xSpace(src[i+len(p)])
}

// applyXmute returns src with one instruction applied. A code mutator runs once
// per value, so the value arrives here.
func applyXmute(src string, x xmuteInst, value string) (string, error) {
	if x.mutator == "identity" {
		return src, nil
	}
	if !x.target.ok {
		return "", fmt.Errorf("%s: the instruction has no target element", x.id)
	}
	if x.attr != "" {
		return applyXMuteAttr(src, x, value)
	}
	switch x.mutator {
	case "code":
		return xSetInner(src, x.target, xEscape(xTextEscaper, value)), nil
	case "empty":
		return xSetInner(src, x.target, ""), nil
	case "whitespace":
		inner := src[x.target.innerStart:x.target.innerEnd]
		return xSetInner(src, x.target, " "+inner+" "), nil
	case "remove":
		return src[:x.target.start] + src[x.target.end:], nil
	}
	return "", fmt.Errorf("%s: unknown mutator %q", x.id, x.mutator)
}

// xSetInner replaces the content of the target element.
func xSetInner(src string, t xmuteTarget, text string) string {
	if t.selfClosing {
		if text == "" {
			return src
		}
		// <a/> has no content to set, so it becomes <a>text</a>.
		return src[:t.tagEnd-2] + ">" + text + "</" + t.name + ">" + src[t.tagEnd:]
	}
	return src[:t.innerStart] + text + src[t.innerEnd:]
}

// xAttr is the byte span of one attribute of a start tag.
type xAttr struct {
	name     string
	start    int // start of the name
	end      int // just after the value
	valStart int // just after the opening quote
	valEnd   int // just before the closing quote
}

// xAttrs lists the attributes of the start tag whose text runs from from to to.
// from must be past the element name.
func xAttrs(src string, from, to int) []xAttr {
	var out []xAttr
	for i := from; i < to; {
		if xSpace(src[i]) || src[i] == '/' || src[i] == '>' {
			i++
			continue
		}
		name, j := xNameAt(src, i)
		if name == "" {
			i++
			continue
		}
		a := xAttr{name: name, start: i, end: j}
		i = j
		for i < to && xSpace(src[i]) {
			i++
		}
		if i < to && src[i] == '=' {
			i++
			for i < to && xSpace(src[i]) {
				i++
			}
			if i < to && (src[i] == '"' || src[i] == '\'') {
				q := src[i]
				k := strings.IndexByte(src[i+1:to], q)
				if k < 0 {
					a.end = to
					out = append(out, a)
					return out
				}
				a.valStart, a.valEnd = i+1, i+1+k
				i = a.valEnd + 1
			}
		}
		a.end = i
		out = append(out, a)
	}
	return out
}

// applyXMuteAttr applies an instruction to one attribute of the target's start
// tag instead of to the element itself.
func applyXMuteAttr(src string, x xmuteInst, value string) (string, error) {
	from := x.target.start + 1 + len(x.target.name)
	var found *xAttr
	attrs := xAttrs(src, from, x.target.tagEnd)
	for i := range attrs {
		if attrs[i].name == x.attr {
			found = &attrs[i]
			break
		}
	}
	if x.mutator == "remove" {
		if found == nil {
			return "", fmt.Errorf("%s: %s has no attribute %q to remove", x.id, x.target.name, x.attr)
		}
		s := found.start
		for s > 0 && xSpace(src[s-1]) {
			s--
		}
		return src[:s] + src[found.end:], nil
	}
	if found == nil {
		// The corpus only edits attributes that are present; adding one keeps
		// the mutation meaningful if a future instance does not.
		at := x.target.tagEnd - 1
		if x.target.selfClosing {
			at = x.target.tagEnd - 2
		}
		v := ""
		if x.mutator == "code" {
			v = xEscape(xAttrEscaper, value)
		}
		return src[:at] + ` ` + x.attr + `="` + v + `"` + src[at:], nil
	}
	if found.valStart == 0 || found.valEnd == 0 {
		return "", fmt.Errorf("%s: attribute %q carries no value to change", x.id, x.attr)
	}
	v := ""
	switch x.mutator {
	case "code":
		v = xEscape(xAttrEscaper, value)
	case "whitespace":
		v = " " + src[found.valStart:found.valEnd] + " "
	}
	return src[:found.valStart] + v + src[found.valEnd:], nil
}

var (
	xTextEscaper = strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;")
	xAttrEscaper = strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;")
)

func xEscape(r *strings.Replacer, s string) string { return r.Replace(s) }

// oneline flattens an instruction for use in a message.
func oneline(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

// xmuteStat records what a replay covered.
type xmuteStat struct {
	Files, Insts, Runs, BundleRuns, Checked, Unevaluated, Failures int
	// Gaps counts the cases where an expectation and the rule set it names
	// genuinely disagree, keyed by case, so the replay reports them instead
	// of absorbing them.
	Gaps map[string]int
	// Unknown counts rule ids the embedded EN 16931 and XRechnung artefacts do
	// not declare: a gap in what this library embeds.
	Unknown map[string]int
	// Unpublished counts rule ids the Peppol bundle does not declare, which is
	// a defect in the upstream expectation rather than in either rule set.
	Unpublished map[string]int
}

// xBundle is one compiled rule-set bundle: the rule sets to run, and every rule
// id they declare.
type xBundle struct {
	sets  []*sch.Schema
	known map[string]bool
}

// xRuleSets compiles a rule-set configuration for both syntaxes. Carrying the
// declared ids lets the harness tell a document that breaks a rule apart from
// an expectation that names a rule the embedded artefacts never contained.
func xRuleSets(t *testing.T, cius CIUS, profile Profile) map[Syntax]xBundle {
	t.Helper()
	out := map[Syntax]xBundle{}
	for _, sx := range []Syntax{SyntaxCII, SyntaxUBL} {
		sets, err := ruleSets(sx, cius, profile)
		if err != nil {
			t.Fatal(err)
		}
		ids := map[string]bool{}
		for _, s := range sets {
			for _, p := range s.Patterns {
				if p.ID != "" {
					ids[p.ID] = true
				}
				for _, r := range p.Rules {
					for _, a := range r.Asserts {
						if a.ID != "" {
							ids[a.ID] = true
						}
					}
				}
			}
		}
		out[sx] = xBundle{sets: sets, known: ids}
	}
	return out
}

// xCounts runs a bundle over an already parsed document and reports how often
// each rule id spoke. A rule that could not be evaluated is a fault of the
// validator, so it is reported as one rather than passed off as silence.
func xCounts(t *testing.T, b xBundle, doc *xp.Doc) map[string]int {
	t.Helper()
	counts := map[string]int{}
	for _, s := range b.sets {
		fs, err := s.Validate(doc, nil)
		if err != nil {
			t.Fatalf("rule set %s: %v", s.Name, err)
		}
		for _, f := range fs {
			if f.Err != "" {
				t.Errorf("rule %s could not be evaluated: %s", f.ID, f.Err)
			}
			if f.ID != "" {
				counts[f.ID]++
			}
		}
	}
	return counts
}

// xVerdict is one side of a comparison: how often each rule id spoke in this
// run and in the unmutated document, the ids its rule sets declare, and
// every id that spoke, for the failure message.
//
// The baseline is what makes the negative half of the suite readable. An
// instance is not required to be a valid document, and upstream asks whether
// a mutation *introduces* a rule rather than whether the document already
// broke it, so a rule that speaks no more often than it did before the
// mutation counts as having kept silent.
type xVerdict struct {
	counts map[string]int
	base   map[string]int
	known  map[string]bool
	ids    []string
}

// xRechnung and xPeppol name the two bundles a replay consults, in messages and
// in the counts of ids they do not declare.
const (
	xRechnung = "XRechnung"
	xPeppol   = "Peppol BIS"
)

// xAnyPeppol reports whether any instruction of a file asserts a PEPPOL-* rule,
// which decides whether the file needs a Peppol baseline at all.
func xAnyPeppol(mutes []xmuteInst) bool {
	for _, x := range mutes {
		if x.namesPeppol() {
			return true
		}
	}
	return false
}

// xmuteGaps lists the cases where an upstream expectation and the rule set it
// names genuinely disagree, keyed by file|instruction|rule. They are written down
// rather than absorbed: the replay counts them as seen but never as passing, and
// an entry that stops matching is an error, so the list cannot quietly rot.
var xmuteGaps = map[string]string{
	"cii-peppol-en16931-r040-HUF-slack.xml|cii-line-charge-amount-slack-HUF|PEPPOL-EN16931-R040":    "the rule hard-codes the slack at 0.02 whatever the currency, so 0.97, 1.03, 0.5 and 1.5 around an expected 1 are breaches in HUF; the expectation assumes the half-unit tolerance BIS 2.x gave a zero-decimal currency",
	"cii-peppol-en16931-r040-HUF-slack.xml|cii-line-allowance-amount-slack-HUF|PEPPOL-EN16931-R040": "the same fixed slack, on the line allowance",
	"cii-peppol-en16931-r040-HUF-slack.xml|cii-charge-amount-slack-HUF|PEPPOL-EN16931-R040":         "the same fixed slack, on the document level charge",
	"cii-peppol-en16931-r040-HUF-slack.xml|cii-allowance-amount-slack-HUF|PEPPOL-EN16931-R040":      "the same fixed slack, on the document level allowance",
	"ubl-inv-peppol-en16931-r008.xml|order-ref-id-empty-valid|PEPPOL-EN16931-R008":                  "the rule is an empty-element rule: context //*[not(*) and not(normalize-space())] with test false(), so emptying OrderReference/ID trips it; the expectation says an optional reference may be emptied",
	"ubl-cn-peppol-en16931-r008.xml|order-ref-id-empty-valid|PEPPOL-EN16931-R008":                   "the same empty-element rule on the credit note",
	"cii-peppol-en16931-r053-1.xml|cii-no-BT-110|PEPPOL-EN16931-R053":                               "the instruction removes the tax total in the invoice currency, which is the one the rule counts, so demanding exactly one makes it speak; BR-CO-15 agrees that the total is mandatory, so the expectation that removing it stays valid cannot hold against either rule set",
	"cii-peppol-en16931-r055.xml|cii-no-BT-110-valid|PEPPOL-EN16931-R055":                           "the sign comparison is made against the removed total, so both arms of the assertion evaluate to the empty sequence and the test is false; the rule is not written for a missing total",
	"cii-br-tmp-3-test.xml|cii-br-tmp-3-case-1-2-only-grossprice|PEPPOL-EN16931-R120":               "the rule binds the base quantity from the net price alone and falls back to 1 without it, so removing that base quantity changes the line total it expects; the expectation assumes the gross price path serves as well",
}

// TestXRechnungXMuteSuite replays the upstream XRechnung test suite: every
// instance of test/instances is mutated once per xmute instruction, the mutated
// document is validated against EN 16931 plus the XRechnung rule set, and the
// rule ids the instruction asserts either have to appear among the findings or
// have to stay absent.
//
// Upstream inherited many of its instances from the Peppol test corpus, so a
// quarter of its expectations name PEPPOL-* rules, which XRechnung does not
// carry. Those cases run the document a second time against the embedded Peppol
// BIS bundle, which is the only place those assertions are defined; the pass is
// gated on the instruction asserting a Peppol id, because one bundle run costs
// roughly what a full validation does.
func TestXRechnungXMuteSuite(t *testing.T) {
	root := filepath.Join(xrefDir(t), "test", "instances")
	xr := xRuleSets(t, CIUSXRechnung, "")
	bs := xRuleSets(t, "", ProfilePeppol)
	st := xmuteStat{Unknown: map[string]int{}, Unpublished: map[string]int{}, Gaps: map[string]int{}}

	for _, sub := range []string{"cii", "ubl-inv", "ubl-cn"} {
		dir := filepath.Join(root, sub)
		want := SyntaxCII
		if sub != "cii" {
			want = SyntaxUBL
		}
		files := glob(t, dir, "*.xml")
		if len(files) == 0 {
			t.Fatalf("no instances in %s", dir)
		}
		for _, f := range files {
			base := filepath.Base(f)
			src := string(read(t, f))
			if sx, err := Detect([]byte(src)); err != nil {
				t.Errorf("%s: %v", base, err)
				continue
			} else if sx != want {
				t.Errorf("%s: %s instance detected as %s", base, sub, sx)
				continue
			}
			mutes, err := xMutes(src)
			if err != nil {
				t.Errorf("%s: %v", base, err)
				continue
			}
			if len(mutes) == 0 {
				t.Errorf("%s: carries no xmute instruction", base)
				continue
			}
			st.Files++
			// The unmutated document is the baseline for the negative half of the
			// suite: a rule it already reports is not this mutation's doing.
			pristine, err := xp.ParseXML(src)
			if err != nil {
				st.Failures++
				t.Errorf("%s: the unmutated instance is not an invoice: %v", base, err)
				continue
			}
			enBase := xCounts(t, xr[want], pristine)
			var ppBase map[string]int
			if xAnyPeppol(mutes) {
				ppBase = xCounts(t, bs[want], pristine)
			}
			for _, x := range mutes {
				st.Insts++
				peppol := x.namesPeppol()
				for _, v := range x.values {
					doc, err := applyXmute(src, x, v)
					if err != nil {
						st.Failures++
						t.Errorf("%s [%s]: %v", base, x.id, err)
						continue
					}
					parsed, err := xp.ParseXML(doc)
					if err != nil {
						st.Failures++
						t.Errorf("%s [%s]: the mutated document is not an invoice: %v", base, x.id, err)
						continue
					}
					rep, err := validateDoc(want, parsed, nil, CIUSXRechnung, "")
					if err != nil {
						st.Failures++
						t.Errorf("%s [%s]: %v", base, x.id, err)
						continue
					}
					st.Runs++
					if rep.Internal > 0 {
						st.Unevaluated += rep.Internal
						for _, fi := range rep.Findings {
							if fi.Err != "" {
								t.Errorf("%s [%s]: rule %s could not be evaluated: %s", base, x.id, fi.ID, fi.Err)
							}
						}
					}
					en := xVerdict{known: xr[want].known, base: enBase, ids: rep.IDs()}
					en.counts = map[string]int{}
					for _, fi := range rep.Findings {
						en.counts[fi.ID]++
					}
					pb := xVerdict{known: bs[want].known, base: ppBase}
					if peppol {
						st.BundleRuns++
						counts := xCounts(t, bs[want], parsed)
						pb.counts = counts
						pb.ids = xmapKeys(counts)
					} else {
						pb.known = nil
					}
					xcheck(t, base, x, v, en, pb, x.invalid, true, &st)
					xcheck(t, base, x, v, en, pb, x.valid, false, &st)
				}
			}
		}
	}

	t.Logf("%d instances, %d instructions, %d mutation runs (%d also replayed against "+
		"the Peppol bundle): %d expectations checked, %d unevaluated rules, %d failures",
		st.Files, st.Insts, st.Runs, st.BundleRuns, st.Checked, st.Unevaluated, st.Failures)
	if len(st.Unknown) > 0 {
		t.Errorf("%d rule id(s) the suite names are absent from the embedded rule sets: %v",
			len(st.Unknown), xstatKeys(st.Unknown))
	}
	if len(st.Unpublished) > 0 {
		t.Logf("%d rule id(s) the suite names are absent from the embedded Peppol bundle: %v",
			len(st.Unpublished), xstatKeys(st.Unpublished))
	}
	for gap, n := range st.Gaps {
		t.Logf("%s spoke %d time(s) against the expectation: %s", gap, n, xmuteGaps[gap])
	}
	for gap := range xmuteGaps {
		if st.Gaps[gap] == 0 {
			t.Errorf("listed gap %s matched no run, so upstream has since changed the expectation or the replay stopped reaching it", gap)
		}
	}
	switch {
	case st.Files < 300:
		t.Fatalf("only %d instances replayed, so the harness itself is broken", st.Files)
	case st.Runs < 1500:
		t.Fatalf("only %d mutation runs, so instructions are being dropped", st.Runs)
	case st.BundleRuns < 400:
		t.Fatalf("only %d runs replayed the Peppol bundle, so Peppol expectations are being dropped", st.BundleRuns)
	case st.Checked < 1600:
		t.Fatalf("only %d expectations checked, so the expectations are not being read", st.Checked)
	}
}

// xcheck compares one expectation list of an instruction against the findings
// of one run. want says whether those rules were expected to fire. Expectations
// naming a Peppol rule are judged against the Peppol bundle instead, because
// XRechnung does not define those assertions.
func xcheck(t *testing.T, base string, x xmuteInst, v string, en, peppol xVerdict, refs []string, want bool, st *xmuteStat) {
	t.Helper()
	for _, id := range refs {
		r, name, unknown := en, xRechnung, &st.Unknown
		if strings.HasPrefix(id, "PEPPOL-") {
			r, name, unknown = peppol, xPeppol, &st.Unpublished
			if r.counts == nil {
				st.Failures++
				t.Errorf("%s [%s %q]: a Peppol expectation was replayed without the Peppol bundle", base, x.id, v)
				continue
			}
		}
		if !r.known[id] {
			(*unknown)[name+" "+id]++
			continue
		}
		if _, ok := xmuteGaps[base+"|"+x.id+"|"+id]; ok {
			// A listed disagreement: reported, never counted as a pass.
			st.Gaps[base+"|"+x.id+"|"+id]++
			continue
		}
		st.Checked++
		if want {
			if r.counts[id] > 0 {
				continue
			}
		} else if r.counts[id] <= r.base[id] {
			continue
		}
		st.Failures++
		verb := "fire"
		if !want {
			verb = "stay silent"
		}
		t.Errorf("%s [%s %q]: expected %s to %s, but it spoke %d time(s) where the "+
			"document speaks it %d time(s); findings are %v",
			base, x.id, v, id, verb, r.counts[id], r.base[id], r.ids)
	}
}

// xmapKeys lists the keys of a counted map in a stable order, for messages.
func xmapKeys(m map[string]int) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// xstatKeys lists the counted rule ids in a stable order.
func xstatKeys(counts map[string]int) []string {
	var out []string
	for id, n := range counts {
		out = append(out, fmt.Sprintf("%s (x%d)", id, n))
	}
	sort.Strings(out)
	return out
}
