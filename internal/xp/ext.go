package xp

import (
	"fmt"
	"maps"
	"strings"
)

// maxCallDepth bounds extension-function recursion. The bodies of a profile
// Schematron's <xsl:function> elements are straight-line, so the cap exists
// only to stop a self-recursive body from hanging the validator.
const maxCallDepth = 50

// VarBinding is one variable declaration inside an extension function body,
// evaluated in document order so that a later binding can read an earlier one.
type VarBinding struct {
	Name string
	E    Expr
}

// FuncDecl is an extension function: parameters, optional local variables and
// one final sequence expression, mirroring the sequence constructor of an
// <xsl:function> element in a Schematron file.
//
// Declare hands back the declaration so the caller can fill Vars and Body once
// every function name is known, which lets functions call each other in any
// order.
type FuncDecl struct {
	// Name is the declared name, "prefix:local" as the schema writes it.
	Name string
	// Params are the parameter names exactly as the body refers to them.
	Params []string
	Vars   []VarBinding
	Body   Expr

	key string
}

// Compiler compiles XPath expressions against one set of namespace bindings and
// one table of extension functions. A Schematron profile that declares its own
// functions compiles every expression - rule contexts, assert tests, let values
// and function bodies - through a single Compiler, so that a call site sees its
// declaration and a body sees the schema's global variables.
type Compiler struct {
	ns  map[string]string
	fns map[string]*FuncDecl
}

// NewCompiler returns a compiler bound to the given prefix to URI map. The map
// is not copied: declarations added later are visible to expressions compiled
// after them, which suits a schema whose <ns> children are read first.
func NewCompiler(ns map[string]string) *Compiler {
	if ns == nil {
		ns = map[string]string{}
	}
	return &Compiler{ns: ns, fns: map[string]*FuncDecl{}}
}

// extKey maps a written name to the expanded-name key it is stored under. A
// name without a prefix, or with an undeclared prefix, is not an extension
// function name and reports false.
func (c *Compiler) extKey(name string) (string, bool) {
	pre, local, found := strings.Cut(name, ":")
	if !found {
		return "", false
	}
	uri, ok := c.ns[pre]
	if !ok {
		return "", false
	}
	return uri + "}" + local, true
}

// Declare registers an extension function whose name must sit in a declared
// namespace. The returned declaration must have Body, and optionally Vars,
// filled in before the function is called.
func (c *Compiler) Declare(name string, params []string) (*FuncDecl, error) {
	key, ok := c.extKey(name)
	if !ok {
		return nil, errf("extension function %s must be in a declared namespace", name)
	}
	if _, dup := c.fns[key]; dup {
		return nil, errf("extension function %s declared twice", name)
	}
	fn := &FuncDecl{Name: name, Params: params, key: key}
	c.fns[key] = fn
	return fn, nil
}

// Func returns the extension function registered under the written name.
func (c *Compiler) Func(name string) (*FuncDecl, bool) {
	key, ok := c.extKey(name)
	if !ok {
		return nil, false
	}
	fn, ok := c.fns[key]
	return fn, ok
}

// Compile parses an expression with this compiler's bindings, so a call to a
// declared extension function resolves and the resulting Expr keeps the
// compiler for the lookup.
func (c *Compiler) Compile(src string) (Expr, error) {
	p := &parser{ns: c.ns, c: c}
	return p.Compile(src)
}

// callFunc evaluates an extension function body. The body has no focus item:
// an XSLT function is called with arguments only, so "." inside a body is an
// error rather than the caller's context node. Parameters shadow globals, and
// the caller's local variables are deliberately not visible.
func callFunc(fn *FuncDecl, args []any, c ctx) (any, error) {
	if c.env.depth >= maxCallDepth {
		return nil, errf("extension function %s: recursion deeper than %d", fn.Name, maxCallDepth)
	}
	if len(args) != len(fn.Params) {
		return nil, errf("extension function %s takes %d arguments, got %d",
			fn.Name, len(fn.Params), len(args))
	}
	if fn.Body.e == nil {
		return nil, errf("extension function %s has no body", fn.Name)
	}
	vars := make(map[string]any, len(c.env.globals)+len(fn.Params)+len(fn.Vars))
	maps.Copy(vars, c.env.globals)
	for i, p := range fn.Params {
		vars[p] = args[i]
	}
	nc := ctx{doc: c.doc, pos: 1, size: 1, vars: vars,
		env: &env{comp: c.env.comp, globals: c.env.globals, depth: c.env.depth + 1}}
	for _, v := range fn.Vars {
		val, err := eval(v.E, nc)
		if err != nil {
			return nil, fmt.Errorf("xp: %s: $%s: %w", fn.Name, v.Name, err)
		}
		vars[v.Name] = val
	}
	return eval(fn.Body.e, nc)
}
