package xp

import (
	"fmt"
	"maps"
	"math"
	"math/big"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

// textNode is a virtual node representing an element's character data. The
// tree keeps text on the element rather than as child nodes, so a text() test
// produces one of these per element that has any, which is all the profile
// rules (ID/text() comparisons, normalize-space(text())) require.
type textNode struct {
	parent *Node
	data   string
}

// attrNode is a virtual node representing an attribute.
type attrNode struct {
	parent *Node
	idx    int
	a      Attr
}

// ctx is the evaluation context.
type ctx struct {
	doc  *Doc
	node any
	pos  int
	size int
	vars map[string]any
	env  *env
}

// env carries the state that stays constant across one evaluation of an
// expression tree: the compiler whose extension functions calls resolve
// against, the global variables an extension function body may read (an XSLT
// function sees globals, never its caller's locals), and the current
// extension-function call depth. It is shared by pointer, so the places that
// derive a child context need no special handling.
type env struct {
	comp    *Compiler
	globals map[string]any
	depth   int
}

// Compile parses an expression.
func (p *parser) Compile(src string) (Expr, error) {
	toks, err := lex(src)
	if err != nil {
		return Expr{}, err
	}
	p.t, p.p, p.src = toks, 0, src
	e, err := p.parseExpr()
	if err != nil {
		return Expr{}, err
	}
	if p.peek().k != tEOF {
		return Expr{}, p.fail("unexpected trailing input")
	}
	return Expr{e: e, src: src, comp: p.c}, nil
}

// Compile parses an expression using the given prefix to URI bindings.
func Compile(src string, ns map[string]string) (Expr, error) {
	pr := &parser{ns: ns}
	return pr.Compile(src)
}

// Expr is a compiled expression.
type Expr struct {
	e    expr
	src  string
	comp *Compiler
}

func (x Expr) String() string { return x.src }

// Eval evaluates the expression with n as context node.
func (x Expr) Eval(doc *Doc, n *Node) (any, error) {
	return x.EvalVars(doc, n, nil)
}

// EvalBool evaluates and coerces to boolean.
func (x Expr) EvalBool(doc *Doc, n *Node) (bool, error) {
	return x.EvalBoolVars(doc, n, nil)
}

// EvalVars evaluates the expression with vars bound as $name. A Schematron
// <let> declaration reaches the evaluator only through this entry point.
func (x Expr) EvalVars(doc *Doc, n *Node, vars map[string]any) (any, error) {
	return x.EvalScoped(doc, n, nil, vars)
}

// EvalScoped evaluates with two variable scopes. globals are visible inside the
// body of an extension function, exactly as an XSLT global variable is; vars are
// visible only to this expression. A Schematron compiler puts schema-, pattern-
// and phase-level <let> declarations in globals and rule-level lets in vars,
// which is how the reference implementation compiles them.
func (x Expr) EvalScoped(doc *Doc, n *Node, globals, vars map[string]any) (any, error) {
	return eval(x.e, ctx{doc: doc, node: n, pos: 1, size: 1, vars: vars,
		env: &env{comp: x.comp, globals: globals}})
}

// EvalBoolScoped evaluates with both scopes bound and coerces the result to
// boolean.
func (x Expr) EvalBoolScoped(doc *Doc, n *Node, globals, vars map[string]any) (bool, error) {
	v, err := x.EvalScoped(doc, n, globals, vars)
	if err != nil {
		return false, err
	}
	return boolean(v), nil
}

// EvalBoolVars evaluates with vars bound and coerces the result to boolean.
func (x Expr) EvalBoolVars(doc *Doc, n *Node, vars map[string]any) (bool, error) {
	v, err := x.EvalVars(doc, n, vars)
	if err != nil {
		return false, err
	}
	return boolean(v), nil
}

func errf(f string, a ...any) error { return fmt.Errorf("xp: "+f, a...) }

// ---------- value helpers ----------

func isSeq(v any) bool { _, ok := v.([]any); return ok }

func nodes(v any) []*Node {
	s, ok := v.([]any)
	if !ok {
		if n, ok := v.(*Node); ok {
			return []*Node{n}
		}
		return nil
	}
	var out []*Node
	for _, x := range s {
		if n, ok := x.(*Node); ok {
			out = append(out, n)
		}
	}
	return out
}

func stringOf(v any) string {
	switch t := v.(type) {
	case nil:
		return ""
	case string:
		return t
	case bool:
		if t {
			return "true"
		}
		return "false"
	case *big.Rat:
		return numString(t)
	case *Node:
		return t.StringValue()
	case *attrNode:
		return t.a.Value
	case *textNode:
		return t.data
	case []any:
		if len(t) == 0 {
			return ""
		}
		return stringOf(t[0])
	}
	return fmt.Sprint(v)
}

// maxRange bounds the number of items a "to" range may produce, so a document
// carrying an absurd bound cannot exhaust memory.
const maxRange = 1 << 20

// evalRange evaluates "low to high". Both operands must be a single number,
// which is truncated to an integer, and the result is the consecutive integers
// between them, empty when low is greater than high.
func evalRange(x *rangeExpr, c ctx) (any, error) {
	lo, err := rangeBound(x.l, c)
	if err != nil {
		return nil, err
	}
	hi, err := rangeBound(x.r, c)
	if err != nil {
		return nil, err
	}
	if hi < lo {
		return []any{}, nil
	}
	if hi-lo+1 > maxRange {
		return nil, fmt.Errorf("xp: range %d to %d is too large", lo, hi)
	}
	out := make([]any, 0, hi-lo+1)
	for i := lo; i <= hi; i++ {
		out = append(out, rat(i))
	}
	return out, nil
}

// rangeBound reduces one range operand to an integer.
func rangeBound(e expr, c ctx) (int64, error) {
	v, err := eval(e, c)
	if err != nil {
		return 0, err
	}
	sq := seqOf(v)
	if len(sq) != 1 {
		return 0, fmt.Errorf("xp: range bound must be a single number, got %d item(s)", len(sq))
	}
	n, ok := numOf(sq[0])
	if !ok {
		return 0, fmt.Errorf("xp: range bound %q is not a number", stringOf(sq[0]))
	}
	return new(big.Int).Quo(n.Num(), n.Denom()).Int64(), nil
}

// temporalRexes are the lexical forms of the three XSD temporal types. A date
// or time in an invoice is untyped text, so castable has to look at the shape
// rather than rely on castTo, which treats temporal values as strings.
var temporalRexes = map[string]*regexp.Regexp{
	"date":     regexp.MustCompile(`^-?[0-9]{4,}-[0-9]{2}-[0-9]{2}([Zz]|[+-][0-9]{2}:[0-9]{2})?$`),
	"dateTime": regexp.MustCompile(`^-?[0-9]{4,}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2}(\.[0-9]+)?([Zz]|[+-][0-9]{2}:[0-9]{2})?$`),
	"time":     regexp.MustCompile(`^([0-9]{2}:[0-9]{2}:[0-9]{2}(\.[0-9]+)?|\-?[0-9]{4,}-[0-9]{2}-[0-9]{2})([Zz]|[+-][0-9]{2}:[0-9]{2})?$`),
}

// integerRex and decimalRex are the lexical forms of the XSD numeric types.
// castTo is lax - it reads any number and keeps it as a rational - so castable
// has to decide for itself whether the text really was an integer.
var integerRex = regexp.MustCompile(`^[+-]?[0-9]+$`)

var decimalRex = regexp.MustCompile(`^[+-]?([0-9]+(\.[0-9]*)?|\.[0-9]+)$`)

// isIntegerType reports whether an atomic type name is one of the integer
// family xs:integer is castable within.
func isIntegerType(name string) bool {
	switch name {
	case "integer", "int", "nonNegativeInteger", "positiveInteger",
		"negativeInteger", "nonPositiveInteger":
		return true
	}
	return false
}

// lexicalCastable reports whether text fits the atomic type it is being cast to.
func lexicalCastable(name, s string) bool {
	if re, ok := temporalRexes[name]; ok {
		return re.MatchString(s)
	}
	if isIntegerType(name) {
		return integerRex.MatchString(s)
	}
	if name == "decimal" {
		return decimalRex.MatchString(s)
	}
	return true
}

// castable reports whether a cast to the atomic type would have succeeded.
// "castable as" always names a single type, so more than one item, or any item
// that does not fit the type, makes it false rather than an error.
func castable(v any, name string) bool {
	sq := seqOf(v)
	if len(sq) > 1 {
		return false
	}
	if _, err := castTo(sq, name); err != nil {
		return false
	}
	for _, it := range sq {
		if r, ok := it.(*big.Rat); ok && isIntegerType(name) && !r.IsInt() {
			return false
		}
		if !lexicalCastable(name, strings.TrimSpace(stringOf(it))) {
			return false
		}
	}
	return true
}

func numString(r *big.Rat) string {
	if r.IsInt() {
		return r.Num().String()
	}
	return strings.TrimRight(strings.TrimRight(r.FloatString(10), "0"), ".")
}

// nanValue is the arithmetic result of an operation with a non-numeric
// operand. Strict XPath raises a dynamic error there, but invoice amounts are
// untyped text and a validator must report the business rule (BR-CO-15 and
// friends) instead of an engine failure, so arithmetic propagates NaN and
// every comparison against NaN is false except "!=" - IEEE 754 semantics,
// which is also what the reference XSLT validators produce.
var nanValue = math.NaN()

func isNaN(v any) bool {
	f, ok := v.(float64)
	return ok && math.IsNaN(f)
}

// numOf coerces to an exact rational; ok=false when not numeric.
func numOf(v any) (*big.Rat, bool) {
	switch t := v.(type) {
	case *big.Rat:
		return t, true
	case float64:
		return nil, false // NaN: only value produced as a float64
	case string:
		return parseNum(t)
	case bool:
		if t {
			return big.NewRat(1, 1), true
		}
		return big.NewRat(0, 1), true
	case *Node:
		return parseNum(strings.TrimSpace(t.StringValue()))
	case *attrNode:
		return parseNum(strings.TrimSpace(t.a.Value))
	case *textNode:
		return parseNum(strings.TrimSpace(t.data))
	case []any:
		if len(t) == 0 {
			return nil, false
		}
		return numOf(t[0])
	}
	return nil, false
}

func parseNum(s string) (*big.Rat, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil, false
	}
	r, ok := new(big.Rat).SetString(s)
	if ok {
		return r, true
	}
	if f, err := strconv.ParseFloat(s, 64); err == nil && !math.IsInf(f, 0) && !math.IsNaN(f) {
		r := new(big.Rat).SetFloat64(f)
		if r != nil {
			return r, true
		}
	}
	return nil, false
}

func boolean(v any) bool {
	switch t := v.(type) {
	case nil:
		return false
	case bool:
		return t
	case string:
		return t != ""
	case *big.Rat:
		return t.Sign() != 0
	case *Node, *attrNode, *textNode:
		return true
	case []any:
		// The effective boolean value of a sequence is that of its first
		// item: a sequence that starts with a node is true, and one that
		// starts with false() is false however long it is.
		if len(t) == 0 {
			return false
		}
		return boolean(t[0])
	}
	return true
}

// ---------- comparison ----------

// atomEq compares two atomic values. A general comparison between two nodes
// compares their string values (identity is only tested with "is"), which is
// what makes predicates such as [@currencyID=$Currency] work when $Currency is
// an element node.
func atomEq(a, b any) bool {
	if isNaN(a) || isNaN(b) {
		return false
	}
	if isNode(a) && isNode(b) {
		return stringOf(a) == stringOf(b)
	}
	as, aIsStr := a.(string)
	bs, bIsStr := b.(string)
	if aIsStr && bIsStr {
		return as == bs
	}
	if aIsStr && isNode(b) {
		return as == stringOf(b)
	}
	if bIsStr && isNode(a) {
		return bs == stringOf(a)
	}
	an, aok := numOf(a)
	bn, bok := numOf(b)
	if aok && bok {
		return an.Cmp(bn) == 0
	}
	return stringOf(a) == stringOf(b)
}

// isEmptySeq reports whether v is the empty sequence.
func isEmptySeq(v any) bool {
	if v == nil {
		return true
	}
	s, ok := v.([]any)
	return ok && len(s) == 0
}

// unwrap reduces a single-item sequence to the item itself, which is what the
// singleton operators ("is", "cast as", value comparisons) expect.
func unwrap(v any) any {
	if s, ok := v.([]any); ok {
		if len(s) == 1 {
			return s[0]
		}
		if len(s) == 0 {
			return nil
		}
	}
	return v
}

func isNode(v any) bool {
	switch v.(type) {
	case *Node, *attrNode, *textNode:
		return true
	}
	return false
}

// general compares two values following XPath existential semantics.
func general(op string, a, b any) (bool, error) {
	if !isSeq(a) && !isSeq(b) {
		return cmpAtom(op, a, b), nil
	}
	var sa, sb []any
	if isSeq(a) {
		sa = a.([]any)
	} else {
		sa = []any{a}
	}
	if isSeq(b) {
		sb = b.([]any)
	} else {
		sb = []any{b}
	}
	if len(sa) == 0 || len(sb) == 0 {
		return false, nil
	}
	// node-set vs node-set with = means identity
	for _, x := range sa {
		for _, y := range sb {
			if cmpAtom(op, x, y) {
				return true, nil
			}
		}
	}
	return false, nil
}

func allNodes(s []any) bool {
	for _, x := range s {
		if !isNode(x) {
			return false
		}
	}
	return true
}

func sameNode(a, b any) bool {
	switch x := a.(type) {
	case *Node:
		y, ok := b.(*Node)
		return ok && x == y
	case *attrNode:
		y, ok := b.(*attrNode)
		return ok && x == y
	}
	return false
}

func cmpAtom(op string, a, b any) bool {
	if isNaN(a) || isNaN(b) {
		// NaN is only ordered against nothing; "!=" is the one comparison a
		// non-numeric amount always satisfies, so a junk total is reported as
		// a mismatch by the balance rules rather than silently passing.
		return op == "!="
	}
	switch op {
	case "=", "!=":
		eq := atomEq(a, b)
		if op == "=" {
			return eq
		}
		return !eq
	}
	an, aok := numOf(a)
	bn, bok := numOf(b)
	if aok && bok {
		c := an.Cmp(bn)
		switch op {
		case "<":
			return c < 0
		case "<=":
			return c <= 0
		case ">":
			return c > 0
		case ">=":
			return c >= 0
		}
	}
	sa, sb := stringOf(a), stringOf(b)
	c := strings.Compare(sa, sb)
	switch op {
	case "<":
		return c < 0
	case "<=":
		return c <= 0
	case ">":
		return c > 0
	case ">=":
		return c >= 0
	}
	return false
}

// ---------- evaluation ----------

// evalWrapped evaluates a public Expr handed back to the engine. Expr
// satisfies this package's private expr interface, so a nested public Expr
// reaches eval: an extension function's local variable holds one, as does any
// sub-expression handed in already compiled. Unwrap it and evaluate in the
// surrounding context.
func evalWrapped(x Expr, c ctx) (any, error) {
	if x.e == nil {
		return nil, errf("expression %q has no compiled body", x.src)
	}
	return eval(x.e, c)
}

// evalSeq evaluates every item of a sequence expression and concatenates the
// results, which is how a union written (a, b) is read.
func evalSeq(x *seqExpr, c ctx) (any, error) {
	var out []any
	for _, it := range x.items {
		v, err := eval(it, c)
		if err != nil {
			return nil, err
		}
		if s2, ok := v.([]any); ok {
			out = append(out, s2...)
			continue
		}
		out = append(out, v)
	}
	return out, nil
}

// evalVar looks a variable up, preferring the binding of the innermost
// enclosing function or FLWOR clause over a global declared by the caller.
func evalVar(x *varExpr, c ctx) (any, error) {
	if v, ok := c.vars[x.name]; ok {
		return v, nil
	}
	if v, ok := c.env.globals[x.name]; ok {
		return v, nil
	}
	return nil, errf("undefined variable $%s", x.name)
}

func evalIf(x *ifExpr, c ctx) (any, error) {
	cv, err := eval(x.cond, c)
	if err != nil {
		return nil, err
	}
	if boolean(cv) {
		return eval(x.a, c)
	}
	return eval(x.b, c)
}

func evalCast(x *castExpr, c ctx) (any, error) {
	v, err := eval(x.x, c)
	if err != nil {
		return nil, err
	}
	return castTo(v, x.name)
}

func evalCastable(x *castableExpr, c ctx) (any, error) {
	v, err := eval(x.x, c)
	if err != nil {
		return nil, err
	}
	return castable(v, x.name), nil
}

// evalUnary applies unary minus and unary plus. An empty sequence stays empty
// rather than turning into zero, and anything that is not a number yields NaN,
// as XPath 2.0 requires.
func evalUnary(x *unExpr, c ctx) (any, error) {
	v, err := eval(x.x, c)
	if err != nil {
		return nil, err
	}
	if isEmptySeq(v) {
		return []any{}, nil
	}
	n, ok := numOf(v)
	if !ok {
		return nanValue, nil
	}
	if x.op == "-" {
		return new(big.Rat).Neg(n), nil
	}
	return n, nil
}

func eval(e expr, c ctx) (any, error) {
	switch x := e.(type) {
	case nil:
		return []any{}, nil
	case Expr:
		return evalWrapped(x, c)
	case *seqExpr:
		return evalSeq(x, c)
	case *litStr:
		return x.s, nil
	case *litNum:
		return x.r, nil
	case *varExpr:
		return evalVar(x, c)
	case *ifExpr:
		return evalIf(x, c)
	case *quantExpr:
		return evalQuant(x, c)
	case *forExpr:
		return evalFor(x, c)
	case *binExpr:
		return evalBin(x, c)
	case *castExpr:
		return evalCast(x, c)

	case *castableExpr:
		return evalCastable(x, c)

	case *rangeExpr:
		return evalRange(x, c)

	case *unExpr:
		return evalUnary(x, c)
	case *pathExpr:
		return evalPath(x, c)
	case *filterExpr:
		return evalFilter(x, c)
	case *funcCall:
		return evalFunc(x, c)
	case *nodeTest:
		return step([]any{c.node}, x, c)
	case *ctxItem:
		return c.node, nil
	}
	return nil, errf("unsupported expression %T", e)
}

// withVar returns a copy of c with name bound. Copying keeps a binding from
// leaking into a sibling iteration or into the caller's <let> variables, and
// binds only the variable: the focus item, position and size stay those of the
// enclosing expression, so relative paths still resolve from the original node.
func withVar(c ctx, name string, v any) ctx {
	vars := make(map[string]any, len(c.vars)+1)
	maps.Copy(vars, c.vars)
	vars[name] = v
	return ctx{doc: c.doc, node: c.node, pos: c.pos, size: c.size, vars: vars, env: c.env}
}

// evalFor evaluates a FLWOR expression as a flat map: each clause nests the
// next, and the return values of the innermost clause are concatenated into one
// sequence rather than nested, as XPath requires.
func evalFor(f *forExpr, c ctx) (any, error) {
	return evalForClause(f, c, 0)
}

func evalForClause(f *forExpr, c ctx, i int) ([]any, error) {
	if i == len(f.clauses) {
		v, err := eval(f.ret, c)
		if err != nil {
			return nil, err
		}
		return seqOf(v), nil
	}
	cl := f.clauses[i]
	set, err := eval(cl.set, c)
	if err != nil {
		return nil, err
	}
	if cl.let {
		return evalForClause(f, withVar(c, cl.name, set), i+1)
	}
	var out []any
	for _, it := range seqOf(set) {
		part, err := evalForClause(f, withVar(c, cl.name, it), i+1)
		if err != nil {
			return nil, err
		}
		out = append(out, part...)
	}
	return out, nil
}

func evalQuant(q *quantExpr, c ctx) (any, error) {
	if q.sat == nil {
		return eval(q.set, c)
	}
	set, err := eval(q.set, c)
	if err != nil {
		return nil, err
	}
	items := seqOf(set)
	sat := q.sat
	for _, it := range items {
		v, err := eval(sat, withVar(c, q.name, it))
		if err != nil {
			return nil, err
		}
		if q.all && !boolean(v) {
			return false, nil
		}
		if !q.all && boolean(v) {
			return true, nil
		}
	}
	return q.all, nil
}

func evalBin(b *binExpr, c ctx) (any, error) {
	l, err := eval(b.l, c)
	if err != nil {
		return nil, err
	}
	switch b.op {
	case "and":
		if !boolean(l) {
			return false, nil
		}
		r, err := eval(b.r, c)
		if err != nil {
			return nil, err
		}
		return boolean(r), nil
	case "or":
		if boolean(l) {
			return true, nil
		}
		r, err := eval(b.r, c)
		if err != nil {
			return nil, err
		}
		return boolean(r), nil
	case "union":
		r, err := eval(b.r, c)
		if err != nil {
			return nil, err
		}
		return seqUnion(l, r), nil
	}
	r, err := eval(b.r, c)
	if err != nil {
		return nil, err
	}
	switch b.op {
	case "=", "!=", "<", "<=", ">", ">=":
		return general(b.op, l, r)
	case "+", "-", "*", "div", "mod", "idiv":
		return arith(b.op, l, r)
	case "is":
		return sameNode(unwrap(l), unwrap(r)), nil
	}
	return nil, errf("unknown operator %q", b.op)
}

// arith evaluates an arithmetic operator. Following XPath 2.0, an empty
// sequence operand yields an empty sequence rather than an error; Schematron
// rules rely on this when optional amounts are missing.
func arith(op string, l, r any) (any, error) {
	if isEmptySeq(l) || isEmptySeq(r) {
		return []any{}, nil
	}
	a, ok := numOf(l)
	if !ok {
		return nanValue, nil
	}
	b, ok := numOf(r)
	if !ok {
		return nanValue, nil
	}
	switch op {
	case "+":
		return new(big.Rat).Add(a, b), nil
	case "-":
		return new(big.Rat).Sub(a, b), nil
	case "*":
		return new(big.Rat).Mul(a, b), nil
	case "div", "idiv":
		if b.Sign() == 0 {
			return nil, errf("division by zero")
		}
		q := new(big.Rat).Quo(a, b)
		if op == "idiv" {
			return truncRat(q), nil
		}
		return q, nil
	case "mod":
		if b.Sign() == 0 {
			return nil, errf("division by zero")
		}
		q := truncRat(new(big.Rat).Quo(a, b))
		return new(big.Rat).Sub(a, new(big.Rat).Mul(q, b)), nil
	}
	return nil, errf("bad arith %s", op)
}

func floorRat(r *big.Rat) *big.Rat {
	q := new(big.Int).Quo(r.Num(), r.Denom())
	if r.Sign() < 0 && !r.IsInt() {
		q.Sub(q, big.NewInt(1))
	}
	return new(big.Rat).SetInt(q)
}

func truncRat(r *big.Rat) *big.Rat {
	return new(big.Rat).SetInt(new(big.Int).Quo(r.Num(), r.Denom()))
}

// roundRat implements fn:round (half toward +infinity) and fn:round-half-even.
func roundRat(r *big.Rat, halfUp bool) *big.Rat {
	half := big.NewRat(1, 2)
	if halfUp {
		return floorRat(new(big.Rat).Add(r, half))
	}
	fl := floorRat(r)
	switch new(big.Rat).Sub(r, fl).Cmp(half) {
	case 1:
		return new(big.Rat).Add(fl, big.NewRat(1, 1))
	case 0:
		if new(big.Int).And(fl.Num(), big.NewInt(1)).Sign() != 0 {
			return new(big.Rat).Add(fl, big.NewRat(1, 1))
		}
	}
	return fl
}

func seqUnion(l, r any) []any {
	var out []any
	add := func(v any) {
		if isSeq(v) {
			for _, x := range v.([]any) {
				out = append(out, x)
			}
			return
		}
		if v == nil {
			return
		}
		out = append(out, v)
	}
	add(l)
	add(r)
	return dedupSort(out)
}

func evalPath(px *pathExpr, c ctx) (any, error) {
	// A lone primary expression (function call, variable, literal, ...) is not
	// a path step: evaluate it in the caller's context so that position() and
	// last() keep the predicate's context.
	if len(px.rest) == 0 && !px.abs {
		switch px.first.(type) {
		case *nodeTest, *filterExpr:
		default:
			return eval(px.first, c)
		}
	}
	var cur []any
	if px.abs {
		cur = []any{c.doc.Root}
	} else {
		cur = []any{c.node}
	}
	all := append([]expr{px.first}, px.rest...)
	// Not a range loop: the body advances i itself when a child step is
	// fused into the current one, which a range loop would ignore.
	for i := 0; i < len(all); i++ { //nolint:modernize // i is advanced in the body
		st := all[i]
		switch s := st.(type) {
		case *nodeTest:
			if nx, ok := fusedChild(all, i); ok {
				nxt, err := stepDescChild(cur, nx, c)
				if err != nil {
					return nil, err
				}
				cur = nxt
				i++ // the child step folded into this one
				continue
			}
			nxt, err := step(cur, s, c)
			if err != nil {
				return nil, err
			}
			cur = nxt
		case *funcCall:
			nxt, err := mapStep(cur, s, c)
			if err != nil {
				return nil, err
			}
			cur = nxt
		case *filterExpr:
			nxt, err := filterStep(cur, s, c)
			if err != nil {
				return nil, err
			}
			cur = nxt
		default:
			// primary expression evaluated against each context node
			var out []any
			for i, n := range cur {
				nc := ctx{doc: c.doc, node: n, pos: i + 1, size: len(cur), vars: c.vars, env: c.env}
				v, err := eval(st, nc)
				if err != nil {
					return nil, err
				}
				out = appendOut(out, v)
			}
			cur = dedupSort(out)
		}
	}
	return cur, nil
}

func appendOut(dst []any, v any) []any {
	if v == nil {
		return dst
	}
	if isSeq(v) {
		return append(dst, v.([]any)...)
	}
	return append(dst, v)
}

func mapStep(cur []any, f *funcCall, c ctx) ([]any, error) {
	var out []any
	for i, n := range cur {
		nc := ctx{doc: c.doc, node: n, pos: i + 1, size: len(cur), vars: c.vars, env: c.env}
		v, err := evalFunc(f, nc)
		if err != nil {
			return nil, err
		}
		out = appendOut(out, v)
	}
	return dedupSort(out), nil
}

func filterStep(cur []any, f *filterExpr, c ctx) ([]any, error) {
	var out []any
	for i, n := range cur {
		nc := ctx{doc: c.doc, node: n, pos: i + 1, size: len(cur), vars: c.vars, env: c.env}
		base, err := evalFilterBase(f.prim, nc)
		if err != nil {
			return nil, err
		}
		res, err := applyPreds(base, f.pred, nc)
		if err != nil {
			return nil, err
		}
		out = appendOut(out, res)
	}
	return dedupSort(out), nil
}

// evalFilterBase evaluates a node test relative to the context node so that
// predicates bind to the axis result rather than to a global set.
func evalFilterBase(p expr, c ctx) (any, error) {
	if nt, ok := p.(*nodeTest); ok {
		return step([]any{c.node}, nt, c)
	}
	return eval(p, c)
}

func evalFilter(f *filterExpr, c ctx) (any, error) {
	base, err := evalFilterBase(f.prim, c)
	if err != nil {
		return nil, err
	}
	if len(f.pred) == 0 {
		return base, nil
	}
	return applyPreds(base, f.pred, c)
}

// applyPreds filters a predicate's base sequence, which may arrive as a single
// atomic item and is handed back the same way it came.
func applyPreds(base any, preds []expr, c ctx) (any, error) {
	items := base
	if !isSeq(items) {
		items = []any{items}
	}
	return applyPredsSeq(items.([]any), preds, c)
}

// applyPredsSeq is the node-set form of applyPreds. A path step always offers
// its candidates as a sequence and wants a sequence back, so this form takes
// and returns []any: passing the slice through an interface boxed it on the
// heap at every step with predicates, which measured a tenth of a document
// validation's CPU.
func applyPredsSeq(cur []any, preds []expr, c ctx) ([]any, error) {
	for _, pr := range preds {
		var keep []any
		for i, it := range cur {
			nc := ctx{doc: c.doc, node: it, pos: i + 1, size: len(cur), vars: c.vars, env: c.env}
			v, err := eval(pr, nc)
			if err != nil {
				return nil, err
			}
			// A predicate whose effective value is a single atomic item is
			// that item; only node/atomic sequences of length != 1 stay wrapped.
			if sq, ok := v.([]any); ok && len(sq) == 1 {
				v = sq[0]
			}
			if n, ok := v.(*big.Rat); ok {
				if n.IsInt() && int(n.Num().Int64()) == i+1 {
					keep = append(keep, it)
				} else if !n.IsInt() {
					if fr, _ := numOf(n); fr != nil {
						f, _ := fr.Float64()
						if math.Abs(f-float64(i+1)) < 1e-9 {
							keep = append(keep, it)
						}
					}
				}
				continue
			}
			if boolean(v) {
				keep = append(keep, it)
			}
		}
		cur = keep
	}
	return cur, nil
}

// fusedChild reports the child step that can be folded into the unpredicated
// descendant-or-self::node() step at index i — that is, a plain "//name" — and
// returns false when the pair has to run as two steps. Folding means the
// intermediate list of every node in the subtree is never built, which for a
// document of this size is the largest temporary allocation the evaluator
// makes. Predicates on the child step keep their meaning: they apply to the
// children of each parent, exactly as they would per context node in a plain
// child step.
func fusedChild(all []expr, i int) (*nodeTest, bool) {
	s, ok := all[i].(*nodeTest)
	if !ok || s.axis != "descendant-or-self" || s.kind != "node" || len(s.pred) > 0 {
		return nil, false
	}
	if i+1 >= len(all) {
		return nil, false
	}
	nx, ok := all[i+1].(*nodeTest)
	if !ok || nx.axis != "child" || nx.kind != "" {
		return nil, false
	}
	return nx, true
}

// stepDescChild collects, for every descendant of each context item, the
// children matching nt, walking the tree with an explicit stack so that no
// intermediate list of all the nodes is built.
func stepDescChild(cur []any, nt *nodeTest, c ctx) ([]any, error) {
	var out []any
	var stack []*Node
	for _, n := range cur {
		el, ok := n.(*Node)
		if !ok {
			continue
		}
		stack = append(stack[:0], el)
		for len(stack) > 0 {
			x := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			start := len(out)
			for _, ch := range x.Children {
				if matchTest(nt, ch) {
					out = append(out, ch)
				}
				stack = append(stack, ch)
			}
			if len(nt.pred) > 0 {
				kept, err := applyPredsSeq(out[start:], nt.pred, c)
				if err != nil {
					return nil, err
				}
				out = append(out[:start], kept...)
			}
		}
	}
	return dedupSort(out), nil
}

// axisStep appends to out every node that the step's axis yields from one
// context node. The axes are a fixed, small universe, so they map onto
// handlers instead of piling up in one switch: adding an axis means adding
// an entry, not reading 150 lines to find the right place.
type axisStep func(nt *nodeTest, n any, out []any) []any

var axisSteps = map[string]axisStep{
	"child":              stepChild,
	"attribute":          stepAttribute,
	"self":               stepSelf,
	"parent":             stepParent,
	"descendant":         stepDescendant,
	"descendant-or-self": stepDescendant,
	"ancestor":           stepAncestor,
	"ancestor-or-self":   stepAncestor,
	"following-sibling":  stepSiblings,
	"preceding-sibling":  stepSiblings,
	"following":          stepFollowing,
	"preceding":          stepFollowing,
}

// step applies one path step: the axis expands each context node to a node
// set, then the step's predicates filter what the axis produced for that node
// alone, which is why the predicate runs per context node and not over the
// merged result.
func step(cur []any, nt *nodeTest, c ctx) ([]any, error) {
	h, ok := axisSteps[nt.axis]
	if !ok {
		return nil, errf("unsupported axis %q", nt.axis)
	}
	var out []any
	for _, n := range cur {
		start := len(out)
		out = h(nt, n, out)
		if len(nt.pred) == 0 {
			continue
		}
		kept, err := applyPredsSeq(out[start:], nt.pred, c)
		if err != nil {
			return nil, err
		}
		out = append(out[:start], kept...)
	}
	return dedupSort(out), nil
}

func stepChild(nt *nodeTest, n any, out []any) []any {
	el, ok := n.(*Node)
	if !ok {
		return out
	}
	if nt.kind == "text" {
		if el.Text != "" {
			out = append(out, &textNode{parent: el, data: el.Text})
		}
		return out
	}
	// comments and processing instructions are dropped by the parser, so
	// those steps can only be empty
	if nt.kind == "comment" || nt.kind == "processing-instruction" {
		return out
	}
	for _, ch := range el.Children {
		if matchTest(nt, ch) {
			out = append(out, ch)
		}
	}
	return out
}

func stepAttribute(nt *nodeTest, n any, out []any) []any {
	el, ok := n.(*Node)
	if !ok {
		return out
	}
	for i, a := range el.Attrs {
		if matchAttr(nt, a) {
			out = append(out, &attrNode{parent: el, idx: i, a: a})
		}
	}
	return out
}

func stepSelf(nt *nodeTest, n any, out []any) []any {
	if matchAny(nt, n) {
		out = append(out, n)
	}
	return out
}

func stepParent(nt *nodeTest, n any, out []any) []any {
	el := elementOf(n)
	if el == nil || el.Parent == nil || el.Parent.IsDoc {
		return out
	}
	if nt.wild || nt.name == "" || matchTest(nt, el.Parent) {
		out = append(out, el.Parent)
	}
	return out
}

func stepDescendant(nt *nodeTest, n any, out []any) []any {
	el, ok := n.(*Node)
	if !ok {
		return out
	}
	if nt.axis == "descendant-or-self" && matchAny(nt, n) {
		out = append(out, n)
	}
	var walk func(*Node)
	walk = func(x *Node) {
		for _, ch := range x.Children {
			if nt.kind == "text" {
				if ch.Text != "" {
					out = append(out, &textNode{parent: ch, data: ch.Text})
				}
			} else if matchTest(nt, ch) {
				out = append(out, ch)
			}
			walk(ch)
		}
	}
	walk(el)
	return out
}

func stepAncestor(nt *nodeTest, n any, out []any) []any {
	el := elementOf(n)
	if el == nil {
		return out
	}
	if nt.axis == "ancestor-or-self" && matchAny(nt, n) {
		out = append(out, n)
	}
	for p := el.Parent; p != nil && !p.IsDoc; p = p.Parent {
		if matchTest(nt, p) {
			out = append(out, p)
		}
	}
	return out
}

func stepSiblings(nt *nodeTest, n any, out []any) []any {
	el, ok := n.(*Node)
	if !ok || el.Parent == nil {
		return out
	}
	following := nt.axis == "following-sibling"
	for i, sib := range el.Parent.Children {
		if sib == el {
			continue
		}
		if (following && i > el.Index) || (!following && i < el.Index) {
			if matchTest(nt, sib) {
				out = append(out, sib)
			}
		}
	}
	return out
}

func stepFollowing(nt *nodeTest, n any, out []any) []any {
	el, ok := n.(*Node)
	if !ok {
		return out // attribute nodes have no preceding/following axis here
	}
	anc := map[*Node]bool{}
	root := el
	for root.Parent != nil {
		anc[root.Parent] = true
		root = root.Parent
	}
	// XPath excludes attribute and namespace nodes from both axes, and
	// ancestors from "preceding" and descendants from "following".
	passed := nt.axis != "following"
	abort := false
	var walk func(x *Node)
	walk = func(x *Node) {
		if abort {
			return
		}
		if x == el {
			if nt.axis == "preceding" {
				abort = true
			} else {
				passed = true // skip the subtree of the context node
			}
			return
		}
		if passed && !anc[x] && matchTest(nt, x) {
			out = append(out, x)
		}
		for _, ch := range x.Children {
			if abort {
				break
			}
			walk(ch)
		}
	}
	walk(root)
	return out
}

// elementOf returns the element a value hangs from: the element itself for an
// element, and the owning element for a virtual attribute or text node.
func elementOf(v any) *Node {
	switch n := v.(type) {
	case *Node:
		if n.IsDoc {
			return nil
		}
		return n
	case *attrNode:
		return n.parent
	case *textNode:
		return n.parent
	}
	return nil
}

func matchTest(nt *nodeTest, n *Node) bool {
	if nt.wild {
		if nt.ns == "" {
			return true
		}
		return n.NS == nt.ns
	}
	return n.Name == nt.name && n.NS == nt.ns
}

func matchAttr(nt *nodeTest, a Attr) bool {
	if nt.wild {
		if nt.ns == "" {
			return true
		}
		return a.NS == nt.ns
	}
	return a.Name == nt.name && a.NS == nt.ns
}

func matchAny(nt *nodeTest, v any) bool {
	switch n := v.(type) {
	case *Node:
		if n.IsDoc {
			// only the node() kind test covers the document node
			return nt.kind == "node"
		}
		return matchTest(nt, n)
	case *attrNode:
		return matchAttr(nt, n.a)
	case *textNode:
		switch nt.kind {
		case "text", "node":
			return true
		case "":
			return nt.wild // "*" covers text, a name test does not
		}
		return false
	}
	return false
}

func ordOf(v any) (int, int) {
	switch n := v.(type) {
	case *Node:
		return n.Ord, 0
	case *attrNode:
		return n.parent.Ord, n.idx + 1
	case *textNode:
		return n.parent.Ord, 1
	}
	return 0, 0
}

// dedupSmall is the node-set size at or below which dedupSort compares items
// with each other rather than building a map: a step usually yields a handful
// of nodes, and hashing those costs more time and more bytes than scanning.
const dedupSmall = 24

// dedupSort returns a unique, document-ordered sequence when all items are
// nodes, otherwise the sequence with duplicate atomics removed per XPath. A
// sequence that is already unique and in document order — which most steps in
// a real evaluation produce — is handed back as it stands, without copying.
func dedupSort(s []any) []any {
	if len(s) < 2 {
		return s
	}
	if allNodes(s) {
		if sortedUnique(s) {
			return s
		}
		return dedupNodes(s)
	}
	// Mixed or atomic sequences: only nodes are deduplicated (by identity).
	// Non-node items may legitimately repeat, and order is preserved.
	seen := make(map[any]bool, len(s))
	u := make([]any, 0, len(s))
	for _, x := range s {
		if isNode(x) {
			if seen[x] {
				continue
			}
			seen[x] = true
		}
		u = append(u, x)
	}
	return u
}

// dedupNodes drops nodes that appear more than once, keeping the first, then
// puts the result in document order.
func dedupNodes(s []any) []any {
	u := make([]any, 0, len(s))
	if len(s) <= dedupSmall {
		for _, x := range s {
			if !holds(u, x) {
				u = append(u, x)
			}
		}
	} else {
		seen := make(map[any]bool, len(s))
		for _, x := range s {
			if !seen[x] {
				seen[x] = true
				u = append(u, x)
			}
		}
	}
	sortNodes(u)
	return u
}

// holds reports whether u already contains x. Nodes are compared by identity,
// which is what membership of an XPath node-set means.
func holds(u []any, x any) bool { return slices.Contains(u, x) }

// sortedUnique reports whether s holds distinct nodes in document order, the
// sequence dedupSort would return unchanged. It is only called when every item
// is a node, so == compares pointers rather than hashing them.
func sortedUnique(s []any) bool {
	for i := 1; i < len(s); i++ {
		if s[i-1] == s[i] {
			return false
		}
		a1, a2 := ordOf(s[i])
		b1, b2 := ordOf(s[i-1])
		if a1 < b1 || (a1 == b1 && a2 < b2) {
			return false
		}
	}
	return true
}

func sortNodes(s []any) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0; j-- {
			a1, a2 := ordOf(s[j])
			b1, b2 := ordOf(s[j-1])
			if a1 < b1 || (a1 == b1 && a2 < b2) {
				s[j], s[j-1] = s[j-1], s[j]
			} else {
				break
			}
		}
	}
}

// castTo implements the XPath 2.0 atomic cast used by "cast as". Casting an
// empty sequence yields an error in a strict processor; Schematron rules such
// as BR-O-08 rely on the cast being applied only to present nodes, so an empty
// sequence is passed through unchanged (comparison then yields false).
func castTo(v any, name string) (any, error) {
	sq, ok := v.([]any)
	if !ok {
		sq = []any{v}
	}
	if len(sq) == 0 {
		return sq, nil
	}
	out := make([]any, 0, len(sq))
	for _, it := range sq {
		switch name {
		case "boolean", "bool":
			switch b := it.(type) {
			case bool:
				out = append(out, b)
			default:
				s := strings.TrimSpace(stringOf(it))
				switch strings.ToLower(s) {
				case "true", "1":
					out = append(out, true)
				case "false", "0", "":
					out = append(out, false)
				default:
					return nil, fmt.Errorf("xp: cannot cast %q to xs:boolean", s)
				}
			}
		case "decimal", "integer", "int", "nonNegativeInteger", "positiveInteger",
			"negativeInteger", "nonPositiveInteger", "double", "float":
			n, ok := numOf(it)
			if !ok {
				return nil, fmt.Errorf("xp: cannot cast %v to xs:%s", stringOf(it), name)
			}
			out = append(out, n)
		case "string", "token", "normalizedString", "NMTOKEN", "ID", "IDREF", "Name", "QName",
			"date", "dateTime", "time", "anyURI":
			out = append(out, stringOf(it))
		default:
			return nil, fmt.Errorf("xp: unsupported cast type xs:%s", name)
		}
	}
	return out, nil
}
