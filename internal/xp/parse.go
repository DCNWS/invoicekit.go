package xp

import (
	"fmt"
	"math/big"
	"strings"
)

type expr interface{ String() string }

type litStr struct{ s string }
type litNum struct{ r *big.Rat }
type varExpr struct{ name string }
type seqExpr struct{ items []expr }
type binExpr struct {
	op string
	l  expr
	r  expr
}
type unExpr struct {
	op string
	x  expr
}
type ifExpr struct{ cond, a, b expr }

// castExpr is "e cast as ns:name" (XPath 2.0 atomic cast).
type castExpr struct {
	x    expr
	ns   string
	name string
}

func (c *castExpr) String() string { return "(cast as xs:" + c.name + ")" }

// castableExpr is "e castable as xs:name": true when the cast would succeed
// instead of raising an error.
type castableExpr struct {
	x    expr
	ns   string
	name string
}

func (c *castableExpr) String() string { return "(castable as xs:" + c.name + ")" }

// rangeExpr is the "to" operator: the consecutive integers from its left
// operand to its right one, both cast to integers.
type rangeExpr struct {
	l, r expr
}

func (c *rangeExpr) String() string { return "(to)" }

type quantExpr struct {
	all  bool // true = every, false = some
	name string
	set  expr
	sat  expr
}

// flworClause is one binding of a FLWOR expression: for iterates over the
// sequence, let binds the whole sequence once.
type flworClause struct {
	name string
	set  expr
	let  bool
}

// forExpr is "for $x in e1 for $y in e2 return e3", optionally with let
// clauses. It is the only way a Schematron rule can build a new sequence, which
// is how the XRechnung CIUS rules rearrange an IBAN into digits.
type forExpr struct {
	clauses []flworClause
	ret     expr
}

// ctxItem is the "." step: the context item itself, which may be a node or any
// atomic value. It is deliberately not a self::node() test, because a predicate
// over a sequence of atomic values, such as the output of fn:tokenize, then
// sees nothing and every starts-with(.) or normalize-space(.) test in it
// silently fails.
type ctxItem struct{}

func (c *ctxItem) String() string { return "." }

type nodeTest struct {
	pred []expr // step predicates (positional semantics per context node)
	axis string // child, self, parent, attribute, ancestor, descendant, descendant-or-self
	name string
	ns   string
	wild bool
	// kind is the node kind for a kind test such as text() or node(); empty
	// for a plain name or wildcard test, which in this tree always means
	// element (or attribute on the attribute axis).
	kind string
}
type filterExpr struct {
	prim expr
	pred []expr
}
type funcCall struct {
	name string
	args []expr
}
type pathExpr struct {
	abs   bool
	first expr
	rest  []expr
}

func (l *litStr) String() string    { return "\"" + l.s + "\"" }
func (n *litNum) String() string    { return n.r.FloatString(6) }
func (v *varExpr) String() string   { return "$" + v.name }
func (s *seqExpr) String() string   { return "seq(...)" }
func (b *binExpr) String() string   { return "(" + b.l.String() + " " + b.op + " " + b.r.String() + ")" }
func (u *unExpr) String() string    { return u.op + u.x.String() }
func (i *ifExpr) String() string    { return "if(...)then...else..." }
func (q *quantExpr) String() string { return "quant $" + q.name }
func (f *forExpr) String() string   { return "for ... return ..." }
func (n *nodeTest) String() string {
	if n.kind != "" {
		return n.axis + "::" + n.kind + "()"
	}
	if n.wild {
		return n.axis + "::*"
	}
	return n.axis + "::" + n.name
}
func (f *filterExpr) String() string { return "filter(...)" }
func (f *funcCall) String() string   { return f.name + "(...)" }
func (p *pathExpr) String() string   { return "path(...)" }

type parser struct {
	t   []token
	p   int
	ns  map[string]string
	src string
	c   *Compiler
}

func (p *parser) peek() token { return p.t[p.p] }

// peekAt looks n tokens ahead; the token list always ends with tEOF.
func (p *parser) peekAt(n int) token {
	if p.p+n >= len(p.t) {
		return token{k: tEOF}
	}
	return p.t[p.p+n]
}
func (p *parser) next() token        { tk := p.t[p.p]; p.p++; return tk }
func (p *parser) isOp(s string) bool { tk := p.peek(); return tk.k == tOp && tk.s == s }

// isMulOp reports whether the next token is a multiplicative operator. "*" is
// lexed as a name token (it doubles as the wildcard node test), so it needs a
// context-sensitive check: after a completed operand it is always multiply.
func (p *parser) isMulOp() bool {
	tk := p.peek()
	return tk.k == tOp && (tk.s == "div" || tk.s == "mod" || tk.s == "idiv") ||
		(tk.k == tName && tk.s == "*")
}

func (p *parser) fail(f string, a ...any) error {
	return fmt.Errorf("xp: parse "+f+" at pos %d in %q", append(a, p.peek().pos, p.src)...)
}

// resolve maps "ram:Foo" to local name + namespace URI.
func (p *parser) resolve(n string) (string, string, error) {
	pre, local, found := strings.Cut(n, ":")
	if found {
		uri, ok := p.ns[pre]
		if !ok {
			return "", "", fmt.Errorf("xp: undefined prefix %q", pre)
		}
		return local, uri, nil
	}
	return n, "", nil
}

func (p *parser) parseExpr() (expr, error) { return p.parseOr() }

func (p *parser) parseOr() (expr, error) {
	l, err := p.parseAnd()
	if err != nil {
		return nil, err
	}
	for p.isOp("or") {
		p.next()
		r, err := p.parseAnd()
		if err != nil {
			return nil, err
		}
		l = &binExpr{op: "or", l: l, r: r}
	}
	return l, nil
}

// opName maps an XPath 2.0 word operator to its symbolic equivalent so the
// evaluator only has to deal with one spelling per comparison.
func opName(s string) string {
	switch s {
	case "eq":
		return "="
	case "ne":
		return "!="
	case "lt":
		return "<"
	case "le":
		return "<="
	case "gt":
		return ">"
	case "ge":
		return ">="
	}
	return s
}

func (p *parser) parseAnd() (expr, error) {
	l, err := p.parseEquality()
	if err != nil {
		return nil, err
	}
	for p.isOp("and") {
		p.next()
		r, err := p.parseEquality()
		if err != nil {
			return nil, err
		}
		l = &binExpr{op: "and", l: l, r: r}
	}
	return l, nil
}

func (p *parser) parseEquality() (expr, error) {
	l, err := p.parseRel()
	if err != nil {
		return nil, err
	}
	for p.isOp("=") || p.isOp("!=") || p.isOp("eq") || p.isOp("ne") {
		op := opName(p.next().s)
		r, err := p.parseRel()
		if err != nil {
			return nil, err
		}
		l = &binExpr{op: op, l: l, r: r}
	}
	return l, nil
}

func (p *parser) parseRel() (expr, error) {
	l, err := p.parseRange()
	if err != nil {
		return nil, err
	}
	for p.isOp("<") || p.isOp("<=") || p.isOp(">") || p.isOp(">=") ||
		p.isOp("lt") || p.isOp("le") || p.isOp("gt") || p.isOp("ge") || p.isOp("is") {
		op := opName(p.next().s)
		r, err := p.parseRange()
		if err != nil {
			return nil, err
		}
		l = &binExpr{op: op, l: l, r: r}
	}
	return l, nil
}

// parseRange parses the "to" range operator, which binds tighter than the
// relational operators: 1 to $n - 1 is the range 1..(n-1).
func (p *parser) parseRange() (expr, error) {
	l, err := p.parseAdd()
	if err != nil {
		return nil, err
	}
	if p.isOp("to") {
		p.next()
		r, err := p.parseAdd()
		if err != nil {
			return nil, err
		}
		return &rangeExpr{l: l, r: r}, nil
	}
	return l, nil
}

func (p *parser) parseAdd() (expr, error) {
	l, err := p.parseMul()
	if err != nil {
		return nil, err
	}
	for p.isOp("+") || p.isOp("-") {
		op := p.next().s
		r, err := p.parseMul()
		if err != nil {
			return nil, err
		}
		l = &binExpr{op: op, l: l, r: r}
	}
	return l, nil
}

func (p *parser) parseMul() (expr, error) {
	l, err := p.parseUnary()
	if err != nil {
		return nil, err
	}
	for p.isMulOp() {
		op := p.next().s
		r, err := p.parseUnary()
		if err != nil {
			return nil, err
		}
		l = &binExpr{op: op, l: l, r: r}
	}
	return l, nil
}

func (p *parser) parseUnary() (expr, error) {
	if p.isOp("-") || p.isOp("+") {
		op := p.next().s
		x, err := p.parseUnary()
		if err != nil {
			return nil, err
		}
		return &unExpr{op: op, x: x}, nil
	}
	x, err := p.parseUnion()
	if err != nil {
		return nil, err
	}
	if x == nil {
		// Every expression funnels through here, so this is the one place that
		// can tell "the operand is missing" from "the operand has ended": a bare
		// "1 +", an empty predicate or a call argument that is not there at all
		// are mistakes, not the empty sequence.
		return nil, p.fail("expected operand")
	}
	if p.isOp("cast") || p.isOp("castable") {
		castable := p.isOp("castable")
		p.next()
		if !p.isOp("as") {
			return nil, p.fail("expected 'as' after 'cast'")
		}
		p.next()
		tk := p.peek()
		if tk.k != tName {
			return nil, p.fail("expected atomic type name")
		}
		p.next()
		local, ns, err := p.resolve(tk.s)
		if err != nil {
			return nil, err
		}
		if castable {
			return &castableExpr{x: x, ns: ns, name: local}, nil
		}
		return &castExpr{x: x, ns: ns, name: local}, nil
	}
	return x, nil
}

func (p *parser) parseUnion() (expr, error) {
	l, err := p.parsePath()
	if err != nil {
		return nil, err
	}
	for p.peek().k == tUnion {
		p.next()
		r, err := p.parsePath()
		if err != nil {
			return nil, err
		}
		l = &binExpr{op: "union", l: l, r: r}
	}
	return l, nil
}

// parsePath parses a location path whose first component is a primary
// expression, plus any following '/' separated steps.
func (p *parser) parsePath() (expr, error) {
	abs := false
	lead := false
	if p.peek().k == tSlash || p.peek().k == tDSlash {
		abs = true
		lead = p.peek().k == tDSlash
		p.next()
	}
	first, err := p.parsePathElt()
	if err != nil {
		return nil, err
	}
	if first == nil {
		if !abs {
			return nil, nil // the next token does not open an expression
		}
		// A bare "/" is the root node and a bare "//" is every descendant of
		// it. Both are legal XPath and both appear in hand-written Schematron.
		axis := "self"
		if lead {
			axis = "descendant-or-self"
		}
		return &pathExpr{abs: true, first: &nodeTest{axis: axis, kind: "node", wild: true}}, nil
	}
	px := &pathExpr{abs: abs, first: first}
	if lead {
		// leading "//" == /descendant-or-self::node()/
		px.rest = append(px.rest, first)
		px.first = &nodeTest{axis: "descendant-or-self", kind: "node", wild: true}
	}
	for p.peek().k == tSlash || p.peek().k == tDSlash || p.isOp("!") {
		dbl := p.peek().k == tDSlash
		p.next()
		st, err := p.parsePathElt()
		if err != nil {
			return nil, err
		}
		if st == nil {
			return nil, p.fail("expected step after '/'")
		}
		if dbl {
			px.rest = append(px.rest, &nodeTest{axis: "descendant-or-self", kind: "node", wild: true})
		}
		px.rest = append(px.rest, st)
	}
	return px, nil
}

// parsePathElt parses one path component: a node test or a primary expression,
// followed by any predicates. A nil expression with a nil error says the next
// token does not open one, which is how the callers decide a step has ended.
func (p *parser) parsePathElt() (expr, error) {
	base, err := p.parsePrimary()
	if err != nil || base == nil {
		return nil, err
	}
	return p.applyPredicates(base)
}

// parsePrimary parses the node test or primary expression that opens a path
// component, before its predicates.
func (p *parser) parsePrimary() (expr, error) {
	tk := p.peek()
	switch tk.k {
	case tName:
		return p.parseNameStep(tk.s)
	case tAt:
		p.next()
		nt, err := p.parseNameOrWild()
		if err != nil {
			return nil, err
		}
		nt.axis = "attribute"
		return nt, nil
	case tDotDot:
		p.next()
		return &nodeTest{axis: "parent"}, nil
	case tDot:
		p.next()
		return &ctxItem{}, nil
	case tLP:
		return p.parseParenExpr()
	case tLB:
		return &ctxItem{}, nil
	case tNum:
		r, ok := new(big.Rat).SetString(tk.num)
		if !ok {
			return nil, p.fail("bad number %s", tk.num)
		}
		p.next()
		return &litNum{r: r}, nil
	case tStr:
		p.next()
		return &litStr{s: tk.s}, nil
	case tVar:
		p.next()
		return &varExpr{name: tk.s}, nil
	case tOp:
		return p.parseOpPrimary(tk.s)
	}
	return nil, nil
}

// parseNameStep parses the steps that open with a name: a node kind test
// (node(), text(), attribute()), an explicit axis (ancestor::), a function call
// or an ordinary element name.
func (p *parser) parseNameStep(name string) (expr, error) {
	if name == "*" {
		p.next()
		return &nodeTest{axis: "child", wild: true, name: "*"}, nil
	}
	p.next()
	if p.isKindTest(name) {
		nt, err := p.parseKindTest(name)
		if err != nil {
			return nil, err
		}
		nt.axis = "child"
		if name == "attribute" {
			nt.axis = "attribute"
		}
		return nt, nil
	}
	if p.peek().k == tColonColon {
		p.next()
		nt, err := p.parseNameOrWild()
		if err != nil {
			return nil, err
		}
		nt.axis = name
		return nt, nil
	}
	if p.peek().k == tLP {
		call := &funcCall{name: name}
		if err := p.parseArgsInto(call); err != nil {
			return nil, err
		}
		return call, nil
	}
	local, ns, err := p.resolve(name)
	if err != nil {
		return nil, err
	}
	return &nodeTest{axis: "child", name: local, ns: ns}, nil
}

// parseParenExpr parses "(" ... ")": the empty sequence, one expression in
// parentheses, or a parenthesised sequence. The last case matters because
// Schematron asserts write (a, b) unions and (..) filters.
func (p *parser) parseParenExpr() (expr, error) {
	p.next()
	if p.peek().k == tRP {
		p.next()
		return &seqExpr{}, nil // the empty sequence
	}
	var items []expr
	for {
		e, err := p.parseExpr()
		if err != nil {
			return nil, err
		}
		items = append(items, e)
		if p.peek().k != tComma {
			break
		}
		p.next()
	}
	if p.peek().k != tRP {
		return nil, p.fail("expected ')'")
	}
	p.next()
	if len(items) == 1 {
		return items[0], nil
	}
	return &seqExpr{items: items}, nil
}

// parseOpPrimary parses the primary expressions that begin with an operator
// word: the conditional, the quantified expressions, the FLWOR expressions and
// unary minus.
func (p *parser) parseOpPrimary(op string) (expr, error) {
	switch op {
	case "if":
		return p.parseIf()
	case "some", "every":
		return p.parseQuant(op == "every")
	case "for", "let":
		return p.parseFor()
	case "-":
		p.next()
		e, err := p.parsePathElt()
		if err != nil {
			return nil, err
		}
		if e == nil {
			return nil, p.fail("expected operand after '-'")
		}
		return &unExpr{op: "-", x: e}, nil
	}
	return nil, nil
}

// applyPredicates attaches the predicates that follow a primary expression. A
// step's predicates belong to the step itself, so positional predicates are
// evaluated relative to each context node. Anything else becomes a filter
// expression with global positional semantics.
func (p *parser) applyPredicates(base expr) (expr, error) {
	for p.peek().k == tLB {
		p.next()
		e, err := p.parseExpr()
		if err != nil {
			return nil, err
		}
		if p.peek().k != tRB {
			return nil, p.fail("expected ']'")
		}
		p.next()
		if nt, ok := base.(*nodeTest); ok {
			nt.pred = append(nt.pred, e)
			continue
		}
		base = &filterExpr{prim: base, pred: []expr{e}}
	}
	return base, nil
}

func (p *parser) parseIf() (expr, error) {
	p.next() // if
	if p.peek().k != tLP {
		return nil, p.fail("expected '(' after if")
	}
	p.next()
	c, err := p.parseExpr()
	if err != nil {
		return nil, err
	}
	if p.peek().k != tRP {
		return nil, p.fail("expected ')' in if")
	}
	p.next()
	if !p.isOp("then") {
		return nil, p.fail("expected then")
	}
	p.next()
	a, err := p.parseExpr()
	if err != nil {
		return nil, err
	}
	if !p.isOp("else") {
		return nil, p.fail("expected else")
	}
	p.next()
	b, err := p.parseExpr()
	if err != nil {
		return nil, err
	}
	return &ifExpr{cond: c, a: a, b: b}, nil
}

func (p *parser) parseQuant(all bool) (expr, error) {
	p.next() // some | every
	var names []string
	var sets []expr
	for {
		if p.peek().k != tVar {
			return nil, p.fail("expected variable after quantifier")
		}
		names = append(names, p.next().s)
		if !p.isOp("in") {
			return nil, p.fail("expected in")
		}
		p.next()
		set, err := p.parseExpr()
		if err != nil {
			return nil, err
		}
		sets = append(sets, set)
		if p.isOp("and") {
			continue
		}
		if !p.isOp("satisfies") {
			return nil, p.fail("expected satisfies")
		}
		p.next()
		sat, err := p.parseExpr()
		if err != nil {
			return nil, err
		}
		q := &quantExpr{all: all, name: names[len(names)-1], set: sets[len(names)-1], sat: sat}
		for i := len(names) - 2; i >= 0; i-- {
			q = &quantExpr{all: all, name: names[i], set: sets[i], sat: q}
		}
		return q, nil
	}
}

// parseFor parses a FLWOR expression: one or more for/let clauses, each of
// which may bind several comma-separated variables, followed by one return
// expression. The clause stop condition matters: a set expression ends where
// the next "for"/"let" keyword or "return" begins, so the set is parsed as an
// ordinary expression and the keywords are never consumed by it.
func (p *parser) parseFor() (expr, error) {
	var clauses []flworClause
	for p.isOp("for") || p.isOp("let") {
		word := p.next().s
		let := word == "let"
		for {
			if p.peek().k != tVar {
				return nil, p.fail("expected variable after %s", word)
			}
			name := p.next().s
			if let {
				if !p.isOp(":=") {
					return nil, p.fail("expected ':=' after let $%s", name)
				}
			} else if !p.isOp("in") {
				return nil, p.fail("expected 'in' after for $%s", name)
			}
			p.next()
			set, err := p.parseExpr()
			if err != nil {
				return nil, err
			}
			clauses = append(clauses, flworClause{name: name, set: set, let: let})
			if p.peek().k != tComma {
				break
			}
			p.next()
		}
	}
	if len(clauses) == 0 {
		return nil, p.fail("expected for or let")
	}
	if !p.isOp("return") {
		return nil, p.fail("expected return")
	}
	p.next()
	ret, err := p.parseExpr()
	if err != nil {
		return nil, err
	}
	return &forExpr{clauses: clauses, ret: ret}, nil
}

func (p *parser) parseArgsInto(f *funcCall) error {
	if p.peek().k != tLP {
		return p.fail("expected '('")
	}
	p.next()
	if p.peek().k != tRP {
		for {
			a, err := p.parseExpr()
			if err != nil {
				return err
			}
			f.args = append(f.args, a)
			if p.peek().k == tComma {
				p.next()
				continue
			}
			break
		}
	}
	if p.peek().k != tRP {
		return p.fail("expected ')' in call %s", f.name)
	}
	p.next()
	return nil
}

// nodeKindTests are the reserved names that introduce a kind test rather than
// a function call or a name test, exactly as the XPath grammar defines them.
var nodeKindTests = map[string]bool{
	"node": true, "text": true, "comment": true, "processing-instruction": true,
	"element": true, "attribute": true,
}

// parseKindTest parses the argument list of a kind test; the name token has
// already been consumed. Only processing-instruction's target is honoured: the
// sequence type argument of element() and attribute() distinguishes types this
// tree does not carry, so it is skipped.
func (p *parser) parseKindTest(kind string) (*nodeTest, error) {
	if p.peek().k != tLP {
		return nil, p.fail("expected '(' after %s", kind)
	}
	p.next()
	nt := &nodeTest{kind: kind, wild: true}
	switch kind {
	case "processing-instruction":
		if p.peek().k == tStr {
			nt.name = p.peek().s
			p.next()
		}
	case "element", "attribute":
		if p.peek().k != tRP {
			nested, err := p.parseNameOrWild()
			if err != nil {
				return nil, err
			}
			nt.name, nt.ns = nested.name, nested.ns
		}
	}
	for depth := 1; depth > 0; {
		switch p.peek().k {
		case tEOF:
			return nil, p.fail("unclosed '%s('", kind)
		case tLP:
			depth++
		case tRP:
			depth--
		}
		if depth == 0 {
			break
		}
		p.next()
	}
	p.next() // the closing ')'
	return nt, nil
}

// isKindTest reports whether the pending name token starts a kind test, which
// the grammar fixes by the '(' that follows the reserved name.
func (p *parser) isKindTest(name string) bool {
	return nodeKindTests[name] && p.peek().k == tLP
}

func (p *parser) parseNameOrWild() (*nodeTest, error) {
	tk := p.peek()
	if tk.k != tName {
		return nil, p.fail("expected node name")
	}
	if nodeKindTests[tk.s] && p.peekAt(1).k == tLP {
		p.next()
		return p.parseKindTest(tk.s)
	}
	p.next()
	if tk.s == "*" {
		return &nodeTest{wild: true, name: "*"}, nil
	}
	if strings.HasSuffix(tk.s, ":*") {
		_, ns, err := p.resolve(tk.s[:len(tk.s)-2])
		if err != nil {
			return nil, err
		}
		return &nodeTest{wild: true, name: "*", ns: ns}, nil
	}
	local, ns, err := p.resolve(tk.s)
	if err != nil {
		return nil, err
	}
	return &nodeTest{name: local, ns: ns}, nil
}
