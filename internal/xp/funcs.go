package xp

import (
	"math/big"
	"regexp"
	"slices"
	"strings"
)

// seqOf normalises an evaluation result into a sequence.
func seqOf(v any) []any {
	if v == nil {
		return nil
	}
	if s, ok := v.([]any); ok {
		return s
	}
	return []any{v}
}

// only returns the first item of a sequence (XPath 1.0 style coercion).
func only(v any) any {
	s := seqOf(v)
	if len(s) == 0 {
		return nil
	}
	return s[0]
}

func local(name string) string {
	_, rest, found := strings.CutLast(name, ":")
	if !found {
		return name
	}
	return rest
}

func isXSCast(name string) bool {
	return strings.HasPrefix(name, "xs:") || strings.HasPrefix(name, "xsd:")
}

// mapStr applies fn to the string value of every item of a sequence.
// Schematron rules preprocessed from XPath 1.0 pass node-sets to string
// functions, so mapping preserves the intended semantics.
func mapStr(v any, fn func(string) any) any {
	s := seqOf(v)
	if len(s) == 0 {
		return nil
	}
	out := make([]any, 0, len(s))
	for _, it := range s {
		out = append(out, fn(stringOf(it)))
	}
	if len(out) == 1 {
		return out[0]
	}
	return out
}

func mapNum(v any, fn func(*big.Rat) any) (any, error) {
	s := seqOf(v)
	if len(s) == 0 {
		return nil, nil
	}
	out := make([]any, 0, len(s))
	for _, it := range s {
		n, ok := numOf(it)
		if !ok {
			out = append(out, nanValue)
			continue
		}
		out = append(out, fn(n))
	}
	if len(out) == 1 {
		return out[0], nil
	}
	return out, nil
}

func rat(v int64) *big.Rat { return new(big.Rat).SetInt64(v) }

var boolWords = map[string]bool{"true": true, "false": false, "1": true, "0": false}

// argv is one builtin call: the evaluated arguments plus the pieces of the
// evaluation context builtins need. The accessor fields keep the table below
// readable. args[i] is the i-th argument; arg(i) yields the empty sequence for
// a missing argument, which is what the specification asks for; focus(i) falls
// back to the context item, which is what the optional arguments of fn:name,
// fn:local-name, fn:string-length, fn:number and fn:data default to in XPath
// 2.0/3.1. The bare name() form matters: the EN 16931 schemas use it in
// //*[ends-with(name(), 'Amount')].
type argv struct {
	name string // the function name as written, prefix included
	xs   bool   // whether that name carried an xs: or xsd: prefix
	narg int    // arguments actually supplied
	args []any  // the evaluated arguments, left to right
	node any    // the context item
	pos  int    // 1-based position of the context item
	size int    // size of the context sequence
}

// arg returns argument i, or the empty sequence when the caller left it out.
func (a *argv) arg(i int) any {
	return nth(a.args, i, nil)
}

// focus returns argument i, or the context item when the caller left it out,
// as the one-argument forms of functions like string() and matches() allow.
func (a *argv) focus(i int) any {
	return nth(a.args, i, a.node)
}

// builtin implements one entry of the builtins table.
type builtin func(a argv) (any, error)

// fnNumber coerces to a number and changes nothing else. It serves both the
// XPath number() and the XML Schema numeric datatypes, which this engine
// represents with one rational type.
func fnNumber(a argv) (any, error) {
	return mapNum(a.arg(0), func(n *big.Rat) any { return n })
}

// fnTrimCast casts to a string, dropping the whitespace the schema types
// allow around the value. It is also the fallback for an unknown xs: name.
func fnTrimCast(a argv) (any, error) {
	return mapStr(a.arg(0), func(s string) any { return strings.TrimSpace(s) }), nil
}

// fnUnsupported reports a function whose name is recognised but whose
// semantics are not implemented, which is a different diagnosis from a name
// nobody ever declared.
func fnUnsupported(a argv) (any, error) {
	return nil, errf("function %s not supported", a.name)
}

// builtins dispatches a call by the local part of its name, whether or not the
// caller qualified it. Functions declared by a schema are checked first, in
// evalFunc.
var builtins = map[string]builtin{
	"not":      func(a argv) (any, error) { return !boolean(a.arg(0)), nil },
	"true":     func(argv) (any, error) { return true, nil },
	"false":    func(argv) (any, error) { return false, nil },
	"boolean":  func(a argv) (any, error) { return boolean(a.arg(0)), nil },
	"exists":   func(a argv) (any, error) { return len(seqOf(a.arg(0))) > 0, nil },
	"empty":    func(a argv) (any, error) { return len(seqOf(a.arg(0))) == 0, nil },
	"count":    func(a argv) (any, error) { return rat(int64(len(seqOf(a.arg(0))))), nil },
	"last":     func(a argv) (any, error) { return rat(int64(a.size)), nil },
	"position": func(a argv) (any, error) { return rat(int64(a.pos)), nil },

	"string": func(a argv) (any, error) {
		if a.narg == 0 {
			return stringOf(a.node), nil
		}
		return mapStr(a.arg(0), func(s string) any { return s }), nil
	},
	"concat": func(a argv) (any, error) {
		var b strings.Builder
		for _, x := range a.args {
			for _, it := range seqOf(x) {
				b.WriteString(stringOf(it))
			}
		}
		return b.String(), nil
	},
	"string-length": func(a argv) (any, error) {
		return mapStr(a.focus(0), func(s string) any { return rat(int64(len([]rune(s)))) }), nil
	},
	"normalize-space": func(a argv) (any, error) {
		if a.narg == 0 {
			return normalizeSpace(stringOf(a.node)), nil
		}
		return mapStr(a.arg(0), func(s string) any { return normalizeSpace(s) }), nil
	},
	"upper-case": func(a argv) (any, error) {
		return mapStr(a.arg(0), func(s string) any { return strings.ToUpper(s) }), nil
	},
	"lower-case": func(a argv) (any, error) {
		return mapStr(a.arg(0), func(s string) any { return strings.ToLower(s) }), nil
	},
	"contains":          func(a argv) (any, error) { return strings.Contains(str(a.arg(0)), str(a.arg(1))), nil },
	"starts-with":       func(a argv) (any, error) { return strings.HasPrefix(str(a.arg(0)), str(a.arg(1))), nil },
	"ends-with":         func(a argv) (any, error) { return strings.HasSuffix(str(a.arg(0)), str(a.arg(1))), nil },
	"substring":         func(a argv) (any, error) { return substring(str(a.arg(0)), a.arg(1), a.arg(2)), nil },
	"normalize-unicode": func(a argv) (any, error) { return str(a.arg(0)), nil },
	"substring-after": func(a argv) (any, error) {
		if _, rest, found := strings.Cut(str(a.arg(0)), str(a.arg(1))); found {
			return rest, nil
		}
		return "", nil
	},
	"substring-before": func(a argv) (any, error) {
		if before, _, found := strings.Cut(str(a.arg(0)), str(a.arg(1))); found {
			return before, nil
		}
		return "", nil
	},
	"translate": func(a argv) (any, error) {
		from, to := str(a.arg(1)), str(a.arg(2))
		return mapStr(a.arg(0), func(s string) any { return translate(from, to, s) }), nil
	},
	"matches": func(a argv) (any, error) {
		return matches(str(a.arg(0)), str(a.arg(1)), str(a.arg(2)))
	},
	"replace": func(a argv) (any, error) {
		return fnReplace(a.arg(0), a.arg(1), a.arg(2), str(a.arg(3)))
	},
	"tokenize": func(a argv) (any, error) {
		re, err := compileXPPattern(str(a.arg(1)), str(a.arg(2)))
		if err != nil {
			return nil, err
		}
		var out []any
		for _, p := range re.Split(str(a.arg(0)), -1) {
			out = append(out, p)
		}
		return out, nil
	},
	"string-to-codepoints": func(a argv) (any, error) { return stringToCodepoints(a.arg(0)), nil },
	"codepoints-to-string": func(a argv) (any, error) { return codepointsToString(a.arg(0)) },

	"sum": func(a argv) (any, error) {
		tot := new(big.Rat)
		for _, it := range seqOf(a.arg(0)) {
			n, ok := numOf(it)
			if !ok {
				return nanValue, nil
			}
			tot.Add(tot, n)
		}
		return tot, nil
	},
	"min": func(a argv) (any, error) { return minmax(a.arg(0), -1) },
	"max": func(a argv) (any, error) { return minmax(a.arg(0), 1) },
	"abs": func(a argv) (any, error) {
		return mapNum(a.arg(0), func(n *big.Rat) any { return new(big.Rat).Abs(n) })
	},
	"round": func(a argv) (any, error) {
		return mapNum(a.arg(0), func(n *big.Rat) any { return roundRat(n, true) })
	},
	"round-half-even": func(a argv) (any, error) {
		return mapNum(a.arg(0), func(n *big.Rat) any { return roundRat(n, false) })
	},
	"floor": func(a argv) (any, error) { return mapNum(a.arg(0), func(n *big.Rat) any { return floorRat(n) }) },
	"ceiling": func(a argv) (any, error) {
		return mapNum(a.arg(0), func(n *big.Rat) any {
			f := floorRat(n)
			if new(big.Rat).Sub(n, f).Sign() != 0 {
				return new(big.Rat).Add(f, rat(1))
			}
			return f
		})
	},
	"number": fnNumber,
	"booleanCast": func(a argv) (any, error) {
		return boolWords[strings.TrimSpace(str(a.arg(0)))], nil
	},

	"distinct-values": func(a argv) (any, error) {
		seen := map[string]bool{}
		var out []any
		for _, it := range seqOf(a.arg(0)) {
			s := stringOf(it)
			if !seen[s] {
				seen[s] = true
				out = append(out, s)
			}
		}
		return out, nil
	},
	"reverse": func(a argv) (any, error) {
		sq := seqOf(a.arg(0))
		out := make([]any, 0, len(sq))
		for _, v := range slices.Backward(sq) {
			out = append(out, v)
		}
		return out, nil
	},
	"string-join": func(a argv) (any, error) {
		var parts []string
		for _, it := range seqOf(a.arg(0)) {
			parts = append(parts, stringOf(it))
		}
		return strings.Join(parts, str(a.arg(1))), nil
	},
	"index-of": func(a argv) (any, error) {
		target := str(a.arg(1))
		var out []any
		for i, it := range seqOf(a.arg(0)) {
			if stringOf(it) == target {
				out = append(out, rat(int64(i+1)))
			}
		}
		return out, nil
	},

	"name":              func(a argv) (any, error) { return nameOf(a.focus(0), true), nil },
	"local-name":        func(a argv) (any, error) { return nameOf(a.focus(0), false), nil },
	"prefix-from-QName": func(a argv) (any, error) { return nameOf(a.arg(0), false), nil },
}

// fnAlias marks names that share an implementation with another entry.
func fnAlias(alias, target string) { builtins[alias] = builtins[target] }

// fnUnsupportedNames lists the recognised-but-unimplemented functions: those
// needing a document store, a namespace resolver or a formatting language.
var fnUnsupportedNames = []string{
	"namespace-uri-for-prefix", "resolve-uri", "doc", "document", "doc-available",
	"unparsed-text", "format-number", "current", "root", "generate-id",
	"compare", "contains-token",
}

// fnNumericNames lists the numeric XML Schema datatypes, all represented by
// one rational number in this engine.
var fnNumericNames = []string{
	"integer", "decimal", "double", "float", "nonNegativeInteger", "positiveInteger",
	"int", "long", "short", "byte", "unsignedInt", "unsignedShort", "gYear", "gMonth",
}

func init() {
	for _, n := range fnUnsupportedNames {
		builtins[n] = fnUnsupported
	}
	for _, n := range fnNumericNames {
		builtins[n] = fnNumber
	}
	for _, pair := range [][2]string{{"data", "boolean"}, {"node-name", "name"}, {"prefixed-name", "name"}} {
		fnAlias(pair[0], pair[1])
	}
}

// evalFunc evaluates a function call. Arguments are evaluated left to right
// first, so an error in an argument is reported before a complaint about the
// function itself.
func evalFunc(f *funcCall, c ctx) (any, error) {
	args, err := evalArgs(f.args, c)
	if err != nil {
		return nil, err
	}
	// A function declared by the schema wins over the builtin table: an
	// extension function name is always prefixed, but the dispatch below looks
	// at the local part only. This is checked before the argument vector is
	// built, since a declared function is called directly.
	if c.env != nil && c.env.comp != nil {
		if fn, ok := c.env.comp.Func(f.name); ok {
			return callFunc(fn, args, c)
		}
	}
	a := argv{
		name: f.name,
		xs:   isXSCast(f.name),
		narg: len(args),
		args: args,
		node: c.node,
		pos:  c.pos,
		size: c.size,
	}
	if b, ok := builtins[local(f.name)]; ok {
		return b(a)
	}
	if a.xs {
		// An unknown XML Schema type name still behaves as a string cast.
		return fnTrimCast(a)
	}
	return nil, errf("unknown function %s", f.name)
}

// nth returns argument i, or dflt when the argument was not supplied.
func nth(args []any, i int, dflt any) any {
	if i < len(args) {
		return args[i]
	}
	return dflt
}

// evalArgs evaluates a call's arguments left to right. A call with no
// arguments needs no slice at all, which is worth checking for: position(),
// last(), true() and the bare numeric datatype names all take none.
func evalArgs(es []expr, c ctx) ([]any, error) {
	if len(es) == 0 {
		return nil, nil
	}
	args := make([]any, 0, len(es))
	for _, e := range es {
		v, err := eval(e, c)
		if err != nil {
			return nil, err
		}
		args = append(args, v)
	}
	return args, nil
}

// translate applies the XPath character mapping: every character of in that
// appears in from is replaced by the character of to at the same position,
// characters of from with no partner in to are dropped, and surplus characters
// of to are ignored. The first occurrence in from wins, as the specification
// requires for duplicates.
func translate(from, to, in string) string {
	fr, tr := []rune(from), []rune(to)
	if len(fr) == 0 {
		return in
	}
	repl := make(map[rune]rune, len(fr))
	drop := make(map[rune]bool, len(fr))
	for i, r := range fr {
		if _, seen := repl[r]; seen || drop[r] {
			continue
		}
		if i < len(tr) {
			repl[r] = tr[i]
			continue
		}
		drop[r] = true
	}
	var b strings.Builder
	for _, r := range in {
		if drop[r] {
			continue
		}
		if m, ok := repl[r]; ok {
			b.WriteRune(m)
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

func str(v any) string { return stringOf(only(v)) }

// String returns the string value of an evaluation result, coercing a sequence
// to its first item. It lets a caller outside this package assemble a
// Schematron message out of expression results.
func String(v any) string { return stringOf(only(v)) }

// Name returns the prefixed name of the first item of an evaluation result,
// which is what a bare <name/> in a Schematron message expands to.
func Name(v any) string {
	n, _ := nameOf(only(v), true).(string)
	return n
}

func normalizeSpace(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

func roundInt(v any) int64 {
	n, ok := numOf(v)
	if !ok {
		return 0
	}
	return roundRat(n, true).Num().Int64()
}

// substring implements fn:substring. XPath positions are 1-based and the
// length is a count of characters, so the slice is [round(start)-1,
// round(start)+round(len)-1) before clamping: substring("12345", 0, 3) is "12".
func substring(s string, a, b any) string {
	r := []rune(s)
	start := roundInt(a)
	lo := start - 1
	hi := int64(len(r))
	if b != nil {
		hi = start + roundInt(b) - 1
	}
	if lo < 0 {
		lo = 0
	}
	if lo > int64(len(r)) {
		lo = int64(len(r))
	}
	if hi < lo {
		hi = lo
	}
	if hi > int64(len(r)) {
		hi = int64(len(r))
	}
	return string(r[lo:hi])
}

func nameOf(v any, prefixed bool) any {
	s := seqOf(v)
	if len(s) == 0 {
		return ""
	}
	switch n := s[0].(type) {
	case *Node:
		if n.IsDoc {
			return ""
		}
		if prefixed && n.Prefix != "" {
			return n.Prefix + ":" + n.Name
		}
		return n.Name
	case *attrNode:
		if prefixed && n.a.NS != "" {
			return n.a.Name
		}
		return n.a.Name
	}
	return stringOf(s[0])
}

func minmax(v any, want int) (any, error) {
	var best *big.Rat
	for _, it := range seqOf(v) {
		n, ok := numOf(it)
		if !ok {
			return nanValue, nil
		}
		if best == nil || n.Cmp(best)*want > 0 {
			best = n
		}
	}
	if best == nil {
		return nil, nil
	}
	return best, nil
}

func matches(s, pat, flags string) (bool, error) {
	re, err := compileXPRegex(pat, flags)
	if err != nil {
		return false, err
	}
	return re.MatchString(s), nil
}

// fnReplace implements fn:replace: every match of the (unanchored) pattern is
// substituted. A sequence input maps over its items, because rules preprocessed
// from XPath 1.0 hand a node-set to the function. In the replacement string
// $0..$9 are capture groups and $$ is a literal $, which is also Go's syntax.
//
// Limits: the flags q (quote-literal) and U (default non-greedy) are not
// supported, and \d, \w and \s are ASCII classes in RE2 rather than the
// Unicode-aware classes of XPath. Neither occurs in the CIUS artefacts.
func fnReplace(in any, pat, rep, flags any) (any, error) {
	if isEmptySeq(in) {
		return nil, nil
	}
	re, err := compileXPPattern(str(pat), str(flags))
	if err != nil {
		return nil, err
	}
	r := str(rep)
	return mapStr(in, func(s string) any { return re.ReplaceAllString(s, r) }), nil
}

// stringToCodepoints implements fn:string-to-codepoints: one integer per
// character. The empty string yields the empty sequence, which is what makes
// it safe to feed an absent element to the IBAN arithmetic.
func stringToCodepoints(v any) []any {
	s := str(v)
	if s == "" {
		return nil
	}
	out := make([]any, 0, len(s))
	for _, r := range s {
		out = append(out, rat(int64(r)))
	}
	return out
}

// codepointsToString implements fn:codepoints-to-string.
func codepointsToString(v any) (any, error) {
	var b strings.Builder
	for _, it := range seqOf(v) {
		n, ok := numOf(it)
		if !ok {
			return nil, errf("codepoints-to-string: %s is not a number", stringOf(it))
		}
		if !n.IsInt() {
			return nil, errf("codepoints-to-string: %s is not an integer", numString(n))
		}
		cp := n.Num().Int64()
		// Surrogates and out-of-range values are errors in XPath; rejecting them
		// beats writing a rune Go would silently replace with U+FFFD.
		if cp < 0 || cp > 0x10FFFF || (cp >= 0xD800 && cp <= 0xDFFF) {
			return nil, errf("codepoints-to-string: %d is not a valid codepoint", cp)
		}
		b.WriteRune(rune(cp))
	}
	return b.String(), nil
}

// compileXPRegex compiles the pattern of fn:matches. Like fn:replace and
// fn:tokenize, fn:matches searches: it is true when the pattern matches any
// part of the input, so a pattern that must cover the whole value anchors
// itself with ^ and $, as every CIUS regex helper does.
func compileXPRegex(pat, flags string) (*regexp.Regexp, error) {
	src, err := xpPattern(pat, flags)
	if err != nil {
		return nil, err
	}
	return regexp.Compile(src)
}

// compileXPPattern maps the few XPath regex constructs that differ from Go's
// RE2 syntax and turns the fn:matches/fn:replace/fn:tokenize flag letters into
// RE2 inline flags. The result is unanchored: fn:matches anchors separately,
// while fn:replace and fn:tokenize must match anywhere.
func xpPattern(pat, flags string) (string, error) {
	// The XPath escapes \s \S \d \D \w \W all exist in RE2, including inside
	// a character class, so the pattern is passed through unchanged: rewriting
	// them textually would break the nested classes profiles write, such as
	// replace($x, '([\s])', ''). The classes stay ASCII-only as in RE2, unlike
	// the Unicode-aware classes of XPath, which no CIUS rule relies on.
	var fl []byte
	for i := range len(flags) {
		switch flags[i] {
		case 'i', 'm', 's', 'x':
			fl = append(fl, flags[i])
		case 'A', 'n':
			// 'A' anchors only fn:matches, which anchors the whole pattern
			// anyway; 'n' asks that . and [^ ] not match a line break, which
			// is already RE2's default.
		default:
			return "", errf("unsupported regular expression flag %q", flags[i])
		}
	}
	// Dot matches every character except a line break unless the caller asked
	// for dotall with the 's' flag, so (?s) is never added on its own: a
	// tokenize($note, "#.+#") would otherwise span the line breaks between the
	// Skonto entries that BR-DE-18 counts.
	if len(fl) == 0 {
		return pat, nil
	}
	return "(?" + string(fl) + ")" + pat, nil
}

// compileXPPattern compiles an unanchored XPath pattern, as fn:replace and
// fn:tokenize need.
func compileXPPattern(pat, flags string) (*regexp.Regexp, error) {
	src, err := xpPattern(pat, flags)
	if err != nil {
		return nil, err
	}
	return regexp.Compile(src)
}
