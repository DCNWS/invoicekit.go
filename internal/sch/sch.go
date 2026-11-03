// Package sch parses ISO Schematron rule files and evaluates them against an
// XML document with the internal XPath subset evaluator. It reads the
// preprocessed flavour the EN 16931 standard publishes as well as the profile
// rule sets maintained downstream (XRechnung, CIUS), which additionally use
// <let> variables, <include>, <xsl:function> helpers, <report> and message
// templates containing <name/> and <value-of/>.
package sch

import (
	"bytes"
	"encoding/xml"
	"fmt"
	"io"
	"slices"
	"strings"

	"github.com/dcnws/invoicekit.go/internal/xp"
)

const (
	// schNS is the Schematron namespace; a file that declares no namespace at
	// all is accepted as Schematron too.
	schNS = "http://purl.oclc.org/dsdl/schematron"
	// xslNS is the XSLT namespace, used only for <xsl:function> helpers.
	xslNS = "http://www.w3.org/1999/XSL/Transform"
	// emptySeq is the XPath expression for the empty sequence, which is what
	// an XSLT instruction sequence with no output produces.
	emptySeq = "()"
	// maxIncludeDepth stops an <include> cycle from recursing forever.
	maxIncludeDepth = 8
)

// Resolver loads an included rule file named by <include href="...">. The href
// is handed over exactly as written, relative to the including file: a resolver
// that serves several directories resolves it itself.
type Resolver func(href string) ([]byte, error)

// Let is one <let name="..." value="..."> variable declaration.
type Let struct {
	Name  string
	Value string

	e *xp.Expr
}

// msgPart is one piece of an assertion message: literal text, a <name/> element
// or a <value-of select="..."> element.
type msgPart struct {
	text string
	name bool   // <name/>: the prefixed name of the context node
	path string // optional path attribute of <name>
	sel  string // select attribute of <value-of>
	e    *xp.Expr
}

// dynamic reports whether the part needs evaluating at validation time.
func (p msgPart) dynamic() bool { return p.name || p.sel != "" }

// Assert is a single <assert> or <report> element.
type Assert struct {
	ID      string
	Flag    string
	Test    string
	Message string
	// Report marks <report>, which fires when its test is true. <assert> fires
	// when its test is false.
	Report bool

	test  *xp.Expr
	parts []msgPart
}

// Rule is a <rule context="..."> element.
type Rule struct {
	Context string
	// Lets are the rule's own <let> declarations: they are evaluated once per
	// context node, in document order, with that node as focus, so a later one
	// can read an earlier one.
	Lets    []*Let
	Asserts []*Assert

	ctx *xp.Expr
}

// Pattern groups rules; phases activate patterns by id. Its Lets are global,
// exactly as in the reference compiler, which emits pattern variables as
// top-level XSLT variables: they are visible in every rule context and test,
// including those of other patterns, and whether or not the pattern is active.
type Pattern struct {
	ID    string
	Lets  []*Let
	Rules []*Rule
}

// Phase lists the patterns it activates. Its Lets are global as well.
type Phase struct {
	ID       string
	Patterns []string
	Lets     []*Let
}

// funcSrc is a parsed <xsl:function> whose expressions are compiled by Compile.
// The declaration is deferred until Compile so that function bodies may call
// each other in any order and see the schema's complete namespace bindings.
type funcSrc struct {
	name   string
	params []string
	seq    string
}

// Schema is a parsed Schematron file.
type Schema struct {
	// Name labels the rule set in findings (for example "en16931" or
	// "xrechnung"), so a caller that runs several rule sets can tell which
	// one spoke. Empty leaves findings unlabelled.
	Name     string
	Title    string
	NS       map[string]string
	Phases   []Phase
	Patterns []*Pattern
	// Lets holds the schema-level (global) variable declarations.
	Lets []*Let
	// Funcs lists the names of the extension functions the file declares.
	Funcs []string

	compiled bool
	errs     []string
	funcs    []*funcSrc
	// glets is every global let in the file: schema, phase and pattern level.
	glets []*Let
}

// Finding is one failed assertion.
type Finding struct {
	ID      string
	Source  string // the Schema.Name of the rule set that produced it
	Flag    string
	Message string
	Context string
	Test    string
	Err     string // non-empty when the rule itself could not be evaluated
}

// Parse reads a Schematron document that includes no other files.
func Parse(data []byte) (*Schema, error) { return ParseWith(data, nil) }

// ParseWith reads a Schematron document, resolving <include href="..."> through
// res. A nil res makes any include an error rather than a silently missing rule
// set.
func ParseWith(data []byte, res Resolver) (*Schema, error) {
	s := &Schema{NS: map[string]string{}}
	if err := s.parseDoc(bytes.NewReader(data), res, 0); err != nil {
		return nil, err
	}
	if len(s.Patterns) == 0 {
		return nil, fmt.Errorf("sch: no patterns found")
	}
	return s, nil
}

// errXML wraps a decoder failure, which always means malformed input.
func errXML(err error) error {
	if err == io.EOF {
		return fmt.Errorf("sch: truncated rule file")
	}
	return fmt.Errorf("sch: xml: %w", err)
}

// parseDoc reads one rule file. depth counts include hops.
func (s *Schema) parseDoc(r io.Reader, res Resolver, depth int) error {
	dec := xml.NewDecoder(r)
	for {
		tk, err := dec.Token()
		if err != nil {
			return errXML(err)
		}
		if se, ok := tk.(xml.StartElement); ok {
			return s.parseRoot(dec, se, res, depth)
		}
	}
}

// parseRoot dispatches on the file's root element. An included file may be a
// bare <pattern>, which is how the XRechnung variable declarations ship.
func (s *Schema) parseRoot(dec *xml.Decoder, se xml.StartElement, res Resolver, depth int) error {
	s.mergeRootNS(se)
	switch se.Name.Local {
	case "schema":
		return s.parseSchema(dec, se, res, depth)
	case "pattern":
		p, err := s.parsePattern(dec, se)
		if err != nil {
			return err
		}
		s.Patterns = append(s.Patterns, p)
		return nil
	}
	return fmt.Errorf("sch: unexpected root element %s", qname(se))
}

// mergeRootNS records the xmlns:prefix declarations of a root element. Profile
// rule files declare their helper-function prefixes only that way, with no
// <ns> child, and an XPath evaluator needs them to resolve the names. Existing
// bindings win, so the including file keeps control.
func (s *Schema) mergeRootNS(se xml.StartElement) {
	for _, a := range se.Attr {
		if a.Name.Space != "xmlns" || a.Name.Local == "" {
			continue
		}
		if _, ok := s.NS[a.Name.Local]; !ok {
			s.NS[a.Name.Local] = a.Value
		}
	}
}

// attrs returns the non-namespace attributes of an element.
func attrs(se xml.StartElement) map[string]string {
	m := make(map[string]string, len(se.Attr))
	for _, a := range se.Attr {
		if a.Name.Space == "xmlns" || a.Name.Local == "xmlns" {
			continue
		}
		m[a.Name.Local] = a.Value
	}
	return m
}

// qname renders an element name with its prefix where one is known.
func qname(se xml.StartElement) string {
	if se.Name.Space != "" && se.Name.Space != schNS {
		return "{" + se.Name.Space + "}" + se.Name.Local
	}
	return se.Name.Local
}

// isSch reports whether an element belongs to the Schematron namespace.
func isSch(se xml.StartElement) bool {
	return se.Name.Space == "" || se.Name.Space == schNS
}

// unsupported rejects any element this reader does not implement. Silently
// skipping one would drop a rule set without anyone noticing.
func unsupported(se xml.StartElement, where string) error {
	return fmt.Errorf("sch: %s is not supported %s", qname(se), where)
}

func (s *Schema) parseSchema(dec *xml.Decoder, root xml.StartElement, res Resolver, depth int) error {
	for {
		tk, err := dec.Token()
		if err != nil {
			return errXML(err)
		}
		switch t := tk.(type) {
		case xml.EndElement:
			if t.Name.Local == root.Name.Local {
				return nil
			}
			return fmt.Errorf("sch: unexpected </%s>", t.Name.Local)
		case xml.StartElement:
			err := s.parseSchemaChild(dec, t, res, depth)
			if err != nil {
				return err
			}
		}
	}
}

func (s *Schema) parseSchemaChild(dec *xml.Decoder, se xml.StartElement, res Resolver, depth int) error {
	if se.Name.Space == xslNS {
		if se.Name.Local != "function" {
			return unsupported(se, "at schema level")
		}
		f, err := s.parseXSLFunction(dec, se)
		if err != nil {
			return err
		}
		s.funcs = append(s.funcs, f)
		s.Funcs = append(s.Funcs, f.name)
		return nil
	}
	if !isSch(se) {
		return unsupported(se, "at schema level")
	}
	a := attrs(se)
	switch se.Name.Local {
	case "title":
		txt, err := elementText(dec)
		if err != nil {
			return err
		}
		if txt != "" {
			s.Title = txt
		}
		return nil
	case "p", "dir": // documentation only
		return skip(dec)
	case "ns":
		if a["prefix"] == "" || a["uri"] == "" {
			return fmt.Errorf("sch: <ns> needs prefix and uri")
		}
		s.NS[a["prefix"]] = a["uri"]
		return skip(dec)
	case "let":
		l, err := parseLet(a)
		if err != nil {
			return err
		}
		s.Lets = append(s.Lets, l)
		return skip(dec)
	case "phase":
		p, err := s.parsePhase(dec, se)
		if err != nil {
			return err
		}
		s.Phases = append(s.Phases, *p)
		return nil
	case "pattern":
		p, err := s.parsePattern(dec, se)
		if err != nil {
			return err
		}
		s.Patterns = append(s.Patterns, p)
		return nil
	case "include":
		return s.parseInclude(dec, se, res, depth, a)
	case "active":
		return fmt.Errorf("sch: <active> outside <phase>")
	case "rule":
		return fmt.Errorf("sch: <rule> outside <pattern>")
	case "assert", "report":
		return fmt.Errorf("sch: <%s> outside <rule>", se.Name.Local)
	}
	return unsupported(se, "at schema level")
}

func (s *Schema) parseInclude(dec *xml.Decoder, se xml.StartElement, res Resolver, depth int, a map[string]string) error {
	if err := skip(dec); err != nil {
		return err
	}
	href := a["href"]
	if href == "" {
		return fmt.Errorf("sch: <include> without href")
	}
	if res == nil {
		return fmt.Errorf("sch: %s includes %q but no resolver was given", qname(se), href)
	}
	if depth >= maxIncludeDepth {
		return fmt.Errorf("sch: include of %q nested more than %d deep", href, maxIncludeDepth)
	}
	data, err := res(href)
	if err != nil {
		return fmt.Errorf("sch: include %q: %w", href, err)
	}
	return s.parseDoc(bytes.NewReader(data), res, depth+1)
}

func (s *Schema) parsePhase(dec *xml.Decoder, se xml.StartElement) (*Phase, error) {
	p := &Phase{ID: attrs(se)["id"]}
	for {
		tk, err := dec.Token()
		if err != nil {
			return nil, errXML(err)
		}
		switch t := tk.(type) {
		case xml.EndElement:
			return p, nil
		case xml.StartElement:
			if !isSch(t) {
				return nil, unsupported(t, "in <phase>")
			}
			a := attrs(t)
			switch t.Name.Local {
			case "active":
				p.Patterns = append(p.Patterns, a["pattern"])
				if err := skip(dec); err != nil {
					return nil, err
				}
			case "let":
				l, err := parseLet(a)
				if err != nil {
					return nil, err
				}
				p.Lets = append(p.Lets, l)
				if err := skip(dec); err != nil {
					return nil, err
				}
			case "title", "p":
				if err := skip(dec); err != nil {
					return nil, err
				}
			default:
				return nil, unsupported(t, "in <phase>")
			}
		}
	}
}

func (s *Schema) parsePattern(dec *xml.Decoder, se xml.StartElement) (*Pattern, error) {
	p := &Pattern{ID: attrs(se)["id"]}
	for {
		tk, err := dec.Token()
		if err != nil {
			return nil, errXML(err)
		}
		switch t := tk.(type) {
		case xml.EndElement:
			return p, nil
		case xml.StartElement:
			if !isSch(t) {
				return nil, unsupported(t, "in <pattern>")
			}
			a := attrs(t)
			switch t.Name.Local {
			case "let":
				l, err := parseLet(a)
				if err != nil {
					return nil, err
				}
				p.Lets = append(p.Lets, l)
				if err := skip(dec); err != nil {
					return nil, err
				}
			case "rule":
				r, err := s.parseRule(dec, t)
				if err != nil {
					return nil, err
				}
				p.Rules = append(p.Rules, r)
			case "title", "p":
				if err := skip(dec); err != nil {
					return nil, err
				}
			case "assert", "report":
				return nil, fmt.Errorf("sch: <%s> outside <rule>", t.Name.Local)
			default:
				return nil, unsupported(t, "in <pattern>")
			}
		}
	}
}

func (s *Schema) parseRule(dec *xml.Decoder, se xml.StartElement) (*Rule, error) {
	a := attrs(se)
	if a["context"] == "" {
		return nil, fmt.Errorf("sch: <rule> without context")
	}
	r := &Rule{Context: a["context"]}
	for {
		tk, err := dec.Token()
		if err != nil {
			return nil, errXML(err)
		}
		switch t := tk.(type) {
		case xml.EndElement:
			return r, nil
		case xml.StartElement:
			if !isSch(t) {
				return nil, unsupported(t, "in <rule>")
			}
			at := attrs(t)
			switch t.Name.Local {
			case "let":
				l, err := parseLet(at)
				if err != nil {
					return nil, err
				}
				r.Lets = append(r.Lets, l)
				if err := skip(dec); err != nil {
					return nil, err
				}
			case "assert", "report":
				as, err := parseAssertion(t, dec)
				if err != nil {
					return nil, err
				}
				r.Asserts = append(r.Asserts, as)
			case "title", "p":
				if err := skip(dec); err != nil {
					return nil, err
				}
			default:
				return nil, unsupported(t, "in <rule>")
			}
		}
	}
}

// parseAssertion reads an <assert> or <report> including its message template.
func parseAssertion(se xml.StartElement, dec *xml.Decoder) (*Assert, error) {
	a := attrs(se)
	if a["test"] == "" {
		return nil, fmt.Errorf("sch: <%s> without test", se.Name.Local)
	}
	as := &Assert{
		ID:     a["id"],
		Flag:   a["flag"],
		Test:   a["test"],
		Report: se.Name.Local == "report",
	}
	if as.Flag == "" {
		as.Flag = "fatal"
	}
	parts, err := messageParts(dec)
	if err != nil {
		return nil, err
	}
	as.parts = parts
	var lit []string
	for _, p := range parts {
		if !p.dynamic() {
			lit = append(lit, p.text)
		}
	}
	as.Message = strings.TrimSpace(strings.Join(lit, ""))
	return as, nil
}

// messageParts collects the mixed content of an assertion element. Only <name>
// and <value-of> are expanded; any other child element is rejected, because a
// silently dropped message part would hide information the reader needs.
func messageParts(dec *xml.Decoder) ([]msgPart, error) {
	var parts []msgPart
	for {
		tk, err := dec.Token()
		if err != nil {
			return nil, errXML(err)
		}
		switch t := tk.(type) {
		case xml.EndElement:
			return parts, nil
		case xml.CharData:
			parts = append(parts, msgPart{text: string(t)})
		case xml.StartElement:
			var p msgPart
			switch {
			case isSch(t) && t.Name.Local == "name":
				p.name = true
				p.path = attrs(t)["path"]
			case isSch(t) && t.Name.Local == "value-of":
				p.sel = attrs(t)["select"]
				if p.sel == "" {
					return nil, fmt.Errorf("sch: <value-of> without select")
				}
			default:
				return nil, unsupported(t, "in an assertion message")
			}
			if err := skip(dec); err != nil {
				return nil, err
			}
			parts = append(parts, p)
		}
	}
}

// parseLet builds a variable declaration from a <let>'s attributes. ISO
// Schematron requires the value attribute; a let whose value is element content
// is rejected rather than guessed at.
func parseLet(a map[string]string) (*Let, error) {
	if a["name"] == "" {
		return nil, fmt.Errorf("sch: <let> without name")
	}
	if a["value"] == "" {
		return nil, fmt.Errorf("sch: <let name=%q> without value", a["name"])
	}
	return &Let{Name: a["name"], Value: a["value"]}, nil
}

// parseXSLFunction reads an <xsl:function> element: its parameters and its
// instruction sequence, which bodyExpr renders as one XPath expression. Shapes
// that expression cannot express (inline templates, literal result elements,
// parameter defaults) are refused rather than approximated.
func (s *Schema) parseXSLFunction(dec *xml.Decoder, se xml.StartElement) (*funcSrc, error) {
	a := attrs(se)
	f := &funcSrc{name: a["name"]}
	if f.name == "" {
		return nil, fmt.Errorf("sch: <xsl:function> without name")
	}
	seq, hasVal, err := s.bodyExpr(dec, f, true)
	if err != nil {
		return nil, err
	}
	if !hasVal {
		return nil, fmt.Errorf("sch: <xsl:function name=%s> has no <xsl:sequence>", f.name)
	}
	f.seq = seq
	return f, nil
}

// bodyExpr consumes the XSLT instructions inside the element whose start tag
// has just been read, up to and including its end tag, and renders them as a
// single XPath expression: local variables become "let" clauses, because in
// XSLT a variable is in scope to the end of its enclosing sequence constructor
// exactly as a let is to the end of its return; each <xsl:sequence> or literal
// text contributes a value, and the values are concatenated like XSLT does.
// hasVal reports whether the sequence produces a value at all.
func (s *Schema) bodyExpr(dec *xml.Decoder, f *funcSrc, top bool) (string, bool, error) {
	var lets, vals []string
	for {
		tk, err := dec.Token()
		if err != nil {
			return "", false, errXML(err)
		}
		switch t := tk.(type) {
		case xml.EndElement:
			expr, hasVal := compose(lets, vals)
			return expr, hasVal, nil
		case xml.CharData:
			txt := string(t)
			if strings.TrimSpace(txt) == "" {
				continue // the whitespace between instructions
			}
			if strings.ContainsAny(txt, "{}") {
				return "", false, fmt.Errorf("sch: literal text with braces in %s is not supported", f.name)
			}
			vals = append(vals, xpathString(txt))
		case xml.StartElement:
			src, err := s.instruction(dec, t, f, top, &lets)
			if err != nil {
				return "", false, err
			}
			if src != "" {
				vals = append(vals, src)
			}
		}
	}
}

// instruction renders one XSLT instruction and returns the expression that
// yields its value; the empty string means the instruction yields nothing, as
// a variable declaration or parameter does. lets collects the local variables
// declared at this level so the enclosing sequence can bind them.
func (s *Schema) instruction(dec *xml.Decoder, se xml.StartElement, f *funcSrc, top bool, lets *[]string) (string, error) {
	if se.Name.Space != xslNS {
		return "", unsupported(se, fmt.Sprintf("in <xsl:function name=%s>", f.name))
	}
	at := attrs(se)
	switch se.Name.Local {
	case "param":
		return "", s.xslParam(dec, at, f, top)
	case "variable":
		return "", s.xslVariable(dec, at, f, lets)
	case "sequence":
		return s.xslSequence(dec, at, f)
	case "choose":
		return s.xslChoose(dec, f)
	case "if":
		return s.xslIf(dec, at, f)
	default:
		return "", unsupported(se, fmt.Sprintf("in <xsl:function name=%s>", f.name))
	}
}

// xslParam records a function parameter. A parameter with a default value is
// rejected: the Schematron artefacts declare parameters without one, and a
// default would need an XPath expression for a case that cannot occur, since
// calls are checked against the declared arity.
func (s *Schema) xslParam(dec *xml.Decoder, at map[string]string, f *funcSrc, top bool) error {
	if at["name"] == "" {
		return fmt.Errorf("sch: <xsl:param> without name in %s", f.name)
	}
	if !top {
		return fmt.Errorf("sch: <xsl:param> inside a nested sequence in %s", f.name)
	}
	_, hasVal, err := s.bodyExpr(dec, f, false)
	if err != nil {
		return err
	}
	if hasVal || at["select"] != "" {
		return fmt.Errorf("sch: <xsl:param name=%s> with a default value in %s is not supported", at["name"], f.name)
	}
	f.params = append(f.params, at["name"])
	return nil
}

// xslVariable binds a local variable. The value is the select attribute, or the
// content when the value is written as an instruction sequence or literal text.
func (s *Schema) xslVariable(dec *xml.Decoder, at map[string]string, f *funcSrc, lets *[]string) error {
	name := at["name"]
	if name == "" {
		return fmt.Errorf("sch: <xsl:variable> without name in %s", f.name)
	}
	body, hasVal, err := s.bodyExpr(dec, f, false)
	if err != nil {
		return err
	}
	val := at["select"]
	switch {
	case hasVal && val != "":
		return fmt.Errorf("sch: <xsl:variable name=%s> has both select and content in %s", name, f.name)
	case hasVal:
		val = body
	case val == "":
		val = emptySeq
	}
	*lets = append(*lets, "$"+name+" := ("+val+")")
	return nil
}

// xslSequence yields its select expression. An <xsl:sequence> with instructions
// inside it would push those results into the output, which the artefacts never
// do and this renderer does not attempt.
func (s *Schema) xslSequence(dec *xml.Decoder, at map[string]string, f *funcSrc) (string, error) {
	_, hasVal, err := s.bodyExpr(dec, f, false)
	if err != nil {
		return "", err
	}
	if hasVal {
		return "", fmt.Errorf("sch: <xsl:sequence> with instructions inside it in %s is not supported", f.name)
	}
	if at["select"] == "" {
		return "", fmt.Errorf("sch: <xsl:sequence> without select in %s", f.name)
	}
	return "(" + at["select"] + ")", nil
}

// xslChoose renders <xsl:choose> as nested conditionals: the branches are
// mutually exclusive and tested in document order, which is exactly how
// if/then/else chains behave.
func (s *Schema) xslChoose(dec *xml.Decoder, f *funcSrc) (string, error) {
	var when []string
	other := emptySeq
	for {
		tk, err := dec.Token()
		if err != nil {
			return "", errXML(err)
		}
		switch t := tk.(type) {
		case xml.EndElement:
			expr := other
			for _, w := range slices.Backward(when) {
				expr = w + " else " + expr
			}
			return expr, nil
		case xml.StartElement:
			if t.Name.Space != xslNS {
				return "", unsupported(t, fmt.Sprintf("in <xsl:choose> in %s", f.name))
			}
			body, err := s.xslBranch(dec, t, f)
			if err != nil {
				return "", err
			}
			if t.Name.Local == "otherwise" {
				other = body
				continue
			}
			when = append(when, body)
		}
	}
}

// xslBranch renders one <xsl:when> or <xsl:otherwise>.
func (s *Schema) xslBranch(dec *xml.Decoder, se xml.StartElement, f *funcSrc) (string, error) {
	if se.Name.Local != "when" && se.Name.Local != "otherwise" {
		return "", unsupported(se, fmt.Sprintf("in <xsl:choose> in %s", f.name))
	}
	body, _, err := s.bodyExpr(dec, f, false)
	if err != nil {
		return "", err
	}
	if se.Name.Local == "otherwise" {
		return paren(body), nil
	}
	test := attrs(se)["test"]
	if test == "" {
		return "", fmt.Errorf("sch: <xsl:when> without test in %s", f.name)
	}
	return fmt.Sprintf("if (%s) then %s", test, paren(body)), nil
}

// xslIf renders <xsl:if>, whose missing otherwise branch produces the empty
// sequence.
func (s *Schema) xslIf(dec *xml.Decoder, at map[string]string, f *funcSrc) (string, error) {
	test := at["test"]
	if test == "" {
		return "", fmt.Errorf("sch: <xsl:if> without test in %s", f.name)
	}
	body, _, err := s.bodyExpr(dec, f, false)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("if (%s) then %s else %s", test, paren(body), emptySeq), nil
}

// compose joins the local variables and value-producing instructions of one
// sequence constructor into a single expression, and reports whether anything
// in it produces a value.
func compose(lets, vals []string) (string, bool) {
	expr := emptySeq
	if len(vals) > 0 {
		expr = "(" + strings.Join(vals, ", ") + ")"
	}
	if len(lets) > 0 {
		expr = "let " + strings.Join(lets, ", ") + " return " + expr
	}
	return expr, len(vals) > 0
}

// paren wraps an expression so it can stand as a branch, and turns an empty
// instruction sequence into the empty sequence.
func paren(src string) string {
	if src == "" {
		return emptySeq
	}
	return "(" + src + ")"
}

// xpathString renders literal text as an XPath string literal, reaching for
// concat only when the text contains both kinds of quote mark.
func xpathString(s string) string {
	if !strings.Contains(s, "'") {
		return "'" + s + "'"
	}
	if !strings.Contains(s, "\"") {
		return "\"" + s + "\""
	}
	parts := strings.Split(s, "'")
	var b strings.Builder
	b.WriteString("concat(")
	for i, p := range parts {
		if i > 0 {
			b.WriteString(", \"'\", ")
		}
		b.WriteString("'" + p + "'")
	}
	b.WriteString(")")
	return b.String()
}

// elementText returns the concatenated character data of an element whose start
// token has just been consumed, leaving the decoder after its end tag.
func elementText(dec *xml.Decoder) (string, error) {
	var b strings.Builder
	for {
		tk, err := dec.Token()
		if err != nil {
			return "", errXML(err)
		}
		switch t := tk.(type) {
		case xml.EndElement:
			return strings.TrimSpace(b.String()), nil
		case xml.CharData:
			b.WriteString(string(t))
		case xml.StartElement:
			if err := skip(dec); err != nil {
				return "", err
			}
		}
	}
}

// skip consumes the rest of an element whose start token has just been read.
func skip(dec *xml.Decoder) error {
	if err := dec.Skip(); err != nil {
		return errXML(err)
	}
	return nil
}

// Compile turns every context, test, let and function body into an XPath
// expression. Expressions the XPath engine cannot parse are collected rather
// than fatal: they are listed by CompileErrors, and Compile keeps reporting
// them, so a schema that failed to compile can never be scored. Expressions
// that parse but fail at runtime are reported per document by Validate.
func (s *Schema) Compile() error {
	if s.compiled {
		return s.compileProblem()
	}
	s.errs = nil
	c := xp.NewCompiler(s.NS)

	// Declare every function before compiling any body: a body may call another
	// function declared later in the file.
	decls := make(map[*funcSrc]*xp.FuncDecl, len(s.funcs))
	for _, f := range s.funcs {
		decl, err := c.Declare(f.name, f.params)
		if err != nil {
			s.errs = append(s.errs, err.Error())
			continue
		}
		decls[f] = decl
	}
	for _, f := range s.funcs {
		decl := decls[f]
		if decl == nil {
			continue
		}
		body, err := c.Compile(f.seq)
		if err != nil {
			s.errs = append(s.errs, fmt.Sprintf("function %s: %v", f.name, err))
			continue
		}
		decl.Body = body
	}

	// Global lets come first: schema level, then every phase and pattern, in
	// declaration order, because a later let may read an earlier one.
	for _, ph := range s.Phases {
		s.glets = append(s.glets, ph.Lets...)
	}
	s.glets = append(s.glets, s.Lets...)
	for _, p := range s.Patterns {
		s.glets = append(s.glets, p.Lets...)
	}
	for _, l := range s.glets {
		e, err := c.Compile(l.Value)
		if err != nil {
			s.errs = append(s.errs, fmt.Sprintf("let $%s: %v", l.Name, err))
			continue
		}
		l.e = &e
	}

	for _, pat := range s.Patterns {
		for _, r := range pat.Rules {
			e, err := c.Compile(anchorContext(r.Context))
			if err != nil {
				s.errs = append(s.errs, fmt.Sprintf("pattern %s: rule %q: context: %v", pat.ID, r.Context, err))
			} else {
				r.ctx = &e
			}
			for _, l := range r.Lets {
				le, err := c.Compile(l.Value)
				if err != nil {
					s.errs = append(s.errs, fmt.Sprintf("pattern %s: rule %q: let $%s: %v",
						pat.ID, r.Context, l.Name, err))
					continue
				}
				l.e = &le
			}
			for _, a := range r.Asserts {
				te, err := c.Compile(a.Test)
				if err != nil {
					s.errs = append(s.errs, fmt.Sprintf("pattern %s: %s %s: test: %v",
						pat.ID, kind(a), a.ID, err))
				} else {
					a.test = &te
				}
				for i := range a.parts {
					p := &a.parts[i]
					src := p.sel
					if p.name {
						src = p.path
					}
					if src == "" {
						continue
					}
					me, err := c.Compile(src)
					if err != nil {
						s.errs = append(s.errs, fmt.Sprintf("pattern %s: %s %s: message: %v",
							pat.ID, kind(a), a.ID, err))
						continue
					}
					p.e = &me
				}
			}
		}
	}
	s.compiled = true
	return s.compileProblem()
}

// compileProblem summarises the collected expression errors, naming how many
// of the schema's expressions are affected and what the first one was.
func (s *Schema) compileProblem() error {
	if len(s.errs) == 0 {
		return nil
	}
	_, a := s.Stats()
	return fmt.Errorf("%d/%d Schematron expressions do not compile (first: %s)",
		len(s.errs), a+len(s.Patterns), s.errs[0])
}

// kind names an assertion element for diagnostics.
func kind(a *Assert) string {
	if a.Report {
		return "report"
	}
	return "assert"
}

// CompileErrors lists expressions the XPath engine could not parse.
func (s *Schema) CompileErrors() []string { return s.errs }

// Stats returns the number of rules and assertions in the schema.
func (s *Schema) Stats() (rules, asserts int) {
	for _, p := range s.Patterns {
		rules += len(p.Rules)
		for _, r := range p.Rules {
			asserts += len(r.Asserts)
		}
	}
	return
}

// PhaseIDs lists all phase ids.
func (s *Schema) PhaseIDs() []string {
	out := make([]string, 0, len(s.Phases))
	for _, p := range s.Phases {
		out = append(out, p.ID)
	}
	return out
}

// anchorContext rewrites a relative rule context so that it matches anywhere in
// the instance tree. The reference Schematron implementation (schxslt) compiles
// <sch:rule context="a/b"> into an XSLT template match pattern, which is tried
// against every node of the document; the EN 16931 schemas rely on that and use
// short relative contexts such as "ram:InvoiceCurrencyCode" for elements buried
// under the header trade settlement. Anchoring at descendant-or-self::node()
// from the root of the tree reproduces that behaviour, and picks up the root
// element itself when the context names it. Union contexts are handled
// alternative by alternative.
func anchorContext(ctx string) string {
	var parts []string
	depth, start := 0, 0
	for i := range len(ctx) {
		switch ctx[i] {
		case '[', '(':
			depth++
		case ']', ')':
			depth--
		case '|':
			if depth == 0 {
				parts = append(parts, ctx[start:i])
				start = i + 1
			}
		}
	}
	parts = append(parts, ctx[start:])
	for i, p := range parts {
		t := strings.TrimSpace(p)
		if t == "" {
			continue
		}
		if strings.HasPrefix(t, "/") || strings.HasPrefix(t, "(") || strings.HasPrefix(t, "$") || t == "." {
			parts[i] = t
			continue
		}
		parts[i] = "//" + t
	}
	return strings.Join(parts, " | ")
}

// Validate evaluates every assertion of the given phases (all phases when none
// are named) against doc. Evaluation errors become findings so that a single
// unsupported construct never silently hides a broken document.
func (s *Schema) Validate(d *xp.Doc, phases []string) ([]Finding, error) {
	// A verdict from a schema that does not compile is not a verdict: it looks
	// like a clean document. Compile is a no-op once the schema is good, so this
	// also catches a caller that ignored the error of an earlier attempt.
	if err := s.Compile(); err != nil {
		return nil, err
	}
	active, err := s.activePatterns(phases)
	if err != nil {
		return nil, err
	}
	var out []Finding
	globals := map[string]any{}
	// Global variables are evaluated once per document, against the document
	// node, and every one of them is evaluated whether or not the pattern that
	// declares it is active: an XSLT global variable is visible everywhere.
	for _, l := range s.glets {
		if l.e == nil {
			continue
		}
		v, err := l.e.EvalScoped(d, d.Root, globals, nil)
		if err != nil {
			out = append(out, Finding{ID: "SCH-LET", Flag: "fatal",
				Context: "global let $" + l.Name, Test: l.Value,
				Message: "global variable could not be evaluated", Err: err.Error()})
			continue
		}
		globals[l.Name] = v
	}
	for _, p := range s.Patterns {
		if !active[p.ID] {
			continue
		}
		for _, r := range p.Rules {
			fs, bad := s.evalRule(d, r, globals)
			out = append(out, fs...)
			if bad != nil {
				out = append(out, *bad)
			}
		}
	}
	if s.Name != "" {
		for i := range out {
			out[i].Source = s.Name
		}
	}
	return out, nil
}

// activePatterns resolves the requested phases into the set of pattern ids to
// run. With no phase named, every pattern is active, including patterns no
// <phase> references.
func (s *Schema) activePatterns(phases []string) (map[string]bool, error) {
	active := map[string]bool{}
	if len(phases) == 0 {
		for _, p := range s.Patterns {
			active[p.ID] = true
		}
		return active, nil
	}
	for _, ph := range phases {
		found := false
		for _, p := range s.Phases {
			if p.ID != ph {
				continue
			}
			found = true
			for _, pat := range p.Patterns {
				active[pat] = true
			}
		}
		if !found {
			return nil, fmt.Errorf("sch: unknown phase %q (have %v)", ph, s.PhaseIDs())
		}
	}
	return active, nil
}

// evalRule runs one rule's assertions against every node its context selects.
// The returned slice keeps the assertion order of the file; the error, if any,
// is a failure of the context expression itself.
func (s *Schema) evalRule(d *xp.Doc, r *Rule, globals map[string]any) ([]Finding, *Finding) {
	var out []Finding
	if r.ctx == nil {
		return out, nil // uncompilable rule, already reported by Compile
	}
	// A rule context is evaluated with the root of the tree as focus, not the
	// document element: that is what makes a relative context such as
	// "ubl-invoice:Invoice | ubl-creditnote:CreditNote" select the root element
	// itself, exactly as the XSLT templates of the reference implementation do.
	v, err := r.ctx.EvalScoped(d, d.Root, globals, nil)
	if err != nil {
		f := Finding{ID: "SCH-RULE", Flag: "fatal",
			Context: r.Context, Test: r.Context,
			Message: "rule context could not be evaluated", Err: err.Error()}
		return nil, &f
	}
	items, ok := v.([]any)
	if !ok {
		items = []any{v}
	}
	if len(items) == 0 {
		return out, nil
	}
	// Rule variables depend on the focus node, so each context node gets its own
	// scope, with its lets bound in declaration order.
	scopes := make([]map[string]any, len(items))
	for i, n := range items {
		nd, ok := n.(*xp.Node)
		if !ok {
			continue
		}
		scope := map[string]any{}
		for _, l := range r.Lets {
			if l.e == nil {
				continue
			}
			lv, err := l.e.EvalScoped(d, nd, globals, scope)
			if err != nil {
				out = append(out, Finding{ID: "SCH-LET", Flag: "fatal",
					Context: r.Context, Test: l.Value,
					Message: "rule variable $" + l.Name + " could not be evaluated",
					Err:     err.Error()})
				scope = nil
				break
			}
			scope[l.Name] = lv
		}
		scopes[i] = scope
	}
	for _, a := range r.Asserts {
		if a.test == nil {
			continue
		}
		for i, n := range items {
			nd, ok := n.(*xp.Node)
			if !ok {
				continue
			}
			locals := scopes[i]
			if locals == nil {
				continue // its rule variables could not be evaluated
			}
			ok2, err := a.test.EvalBoolScoped(d, nd, globals, locals)
			if err != nil {
				out = append(out, Finding{ID: a.ID, Flag: a.Flag,
					Context: r.Context, Test: a.Test,
					Message: "assert test could not be evaluated", Err: err.Error()})
				break // report the eval error once per assertion
			}
			fires := !ok2
			if a.Report {
				fires = ok2
			}
			if !fires {
				continue
			}
			out = append(out, Finding{ID: a.ID, Flag: a.Flag,
				Context: r.Context, Test: a.Test,
				Message: a.render(d, nd, globals, locals)})
		}
	}
	return out, nil
}

// render expands <name/> and <value-of/> in the message against the context
// node. A part that cannot be evaluated falls back to the literal text, since
// the assertion has already failed and its message must still be readable.
func (a *Assert) render(d *xp.Doc, n *xp.Node, globals, locals map[string]any) string {
	dyn := false
	for _, p := range a.parts {
		if p.dynamic() {
			dyn = true
			break
		}
	}
	if !dyn {
		return a.Message
	}
	var b strings.Builder
	for _, p := range a.parts {
		switch {
		case p.dynamic():
			if p.name && p.path == "" {
				b.WriteString(xp.Name(n))
				continue
			}
			if p.e == nil {
				continue
			}
			v, err := p.e.EvalScoped(d, n, globals, locals)
			if err != nil {
				continue
			}
			if p.name {
				b.WriteString(xp.Name(v))
			} else {
				b.WriteString(xp.String(v))
			}
		default:
			b.WriteString(p.text)
		}
	}
	return strings.TrimSpace(b.String())
}
