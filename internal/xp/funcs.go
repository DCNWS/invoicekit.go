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

func evalFunc(f *funcCall, c ctx) (any, error) {
	name := local(f.name)
	xs := isXSCast(f.name)
	args := make([]any, 0, len(f.args))
	for _, a := range f.args {
		v, err := eval(a, c)
		if err != nil {
			return nil, err
		}
		args = append(args, v)
	}
	// A missing optional argument is the empty sequence, not the focus node;
	// functions that take an implicit focus argument test narg explicitly.
	arg := func(i int) any {
		if i < len(args) {
			return args[i]
		}
		return nil
	}
	narg := len(args)
	// focus returns argument i, defaulting to the context item when the
	// argument was omitted. In XPath 2.0/3.1 fn:name, fn:local-name,
	// fn:string-length, fn:number and fn:data all take an optional argument
	// whose default is the focus node, and the EN 16931 schemas rely on the
	// bare name() form (e.g. //*[ends-with(name(), 'Amount')]).
	focus := func(i int) any {
		if i < len(args) {
			return args[i]
		}
		return c.node
	}

	// A function declared by the schema wins over the builtin table: an
	// extension function name is always prefixed, but the dispatch below looks
	// at the local part only.
	if c.env != nil && c.env.comp != nil {
		if fn, ok := c.env.comp.Func(f.name); ok {
			return callFunc(fn, args, c)
		}
	}

	switch name {
	case "not":
		return !boolean(arg(0)), nil
	case "true":
		return true, nil
	case "false":
		return false, nil
	case "boolean", "data":
		return boolean(arg(0)), nil
	case "exists":
		return len(seqOf(arg(0))) > 0, nil
	case "empty":
		return len(seqOf(arg(0))) == 0, nil
	case "count":
		return rat(int64(len(seqOf(arg(0))))), nil
	case "string":
		if narg == 0 {
			return stringOf(c.node), nil
		}
		return mapStr(args[0], func(s string) any { return s }), nil
	case "concat":
		var b strings.Builder
		for _, a := range args {
			for _, it := range seqOf(a) {
				b.WriteString(stringOf(it))
			}
		}
		return b.String(), nil
	case "string-length":
		return mapStr(focus(0), func(s string) any { return rat(int64(len([]rune(s)))) }), nil
	case "normalize-space":
		if narg == 0 {
			return normalizeSpace(stringOf(c.node)), nil
		}
		return mapStr(args[0], func(s string) any { return normalizeSpace(s) }), nil
	case "upper-case":
		return mapStr(arg(0), func(s string) any { return strings.ToUpper(s) }), nil
	case "lower-case":
		return mapStr(arg(0), func(s string) any { return strings.ToLower(s) }), nil
	case "contains":
		return strings.Contains(str(arg(0)), str(arg(1))), nil
	case "starts-with":
		return strings.HasPrefix(str(arg(0)), str(arg(1))), nil
	case "ends-with":
		return strings.HasSuffix(str(arg(0)), str(arg(1))), nil
	case "substring-after":
		a, b := str(arg(0)), str(arg(1))
		if _, rest, found := strings.Cut(a, b); found {
			return rest, nil
		}
		return "", nil
	case "substring-before":
		a, b := str(arg(0)), str(arg(1))
		if before, _, found := strings.Cut(a, b); found {
			return before, nil
		}
		return "", nil
	case "substring":
		return substring(str(arg(0)), arg(1), arg(2)), nil
	case "translate":
		return mapStr(args[0], func(s string) any {
			return translate(str(arg(1)), str(arg(2)), s)
		}), nil
	case "matches":
		return matches(str(arg(0)), str(arg(1)), str(arg(2)))
	case "replace":
		return fnReplace(arg(0), arg(1), arg(2), str(arg(3)))
	case "tokenize":
		re, err := compileXPPattern(str(arg(1)), str(arg(2)))
		if err != nil {
			return nil, err
		}
		var out []any
		for _, p := range re.Split(str(arg(0)), -1) {
			out = append(out, p)
		}
		return out, nil
	case "normalize-unicode":
		return str(arg(0)), nil
	case "sum":
		tot := new(big.Rat)
		for _, it := range seqOf(arg(0)) {
			n, ok := numOf(it)
			if !ok {
				return nanValue, nil
			}
			tot.Add(tot, n)
		}
		return tot, nil
	case "min":
		return minmax(arg(0), -1)
	case "max":
		return minmax(arg(0), 1)
	case "abs":
		return mapNum(arg(0), func(n *big.Rat) any { return new(big.Rat).Abs(n) })
	case "round":
		return mapNum(arg(0), func(n *big.Rat) any { return roundRat(n, true) })
	case "round-half-even":
		return mapNum(arg(0), func(n *big.Rat) any { return roundRat(n, false) })
	case "floor":
		return mapNum(arg(0), func(n *big.Rat) any { return floorRat(n) })
	case "ceiling":
		return mapNum(arg(0), func(n *big.Rat) any {
			f := floorRat(n)
			if new(big.Rat).Sub(n, f).Sign() != 0 {
				return new(big.Rat).Add(f, rat(1))
			}
			return f
		})
	case "number":
		_ = focus
		return mapNum(arg(0), func(n *big.Rat) any { return n })
	case "integer", "decimal", "double", "float", "nonNegativeInteger", "positiveInteger",
		"int", "long", "short", "byte", "unsignedInt", "unsignedShort", "gYear", "gMonth":
		return mapNum(arg(0), func(n *big.Rat) any { return n })
	case "booleanCast":
		return boolWords[strings.TrimSpace(str(arg(0)))], nil
	case "date", "dateTime", "time", "anyURI", "QName", "ID", "IDREF", "base64Binary", "hexBinary", "duration":
		if xs && name != "string" {
			return mapStr(arg(0), func(s string) any { return strings.TrimSpace(s) }), nil
		}
	case "distinct-values":
		seen := map[string]bool{}
		var out []any
		for _, it := range seqOf(arg(0)) {
			s := stringOf(it)
			if !seen[s] {
				seen[s] = true
				out = append(out, s)
			}
		}
		return out, nil
	case "string-to-codepoints":
		return stringToCodepoints(arg(0)), nil
	case "codepoints-to-string":
		return codepointsToString(arg(0))
	case "reverse":
		sq := seqOf(arg(0))
		out := make([]any, 0, len(sq))
		for _, v := range slices.Backward(sq) {
			out = append(out, v)
		}
		return out, nil
	case "string-join":
		sep := ""
		if narg > 1 {
			sep = str(args[1])
		}
		var parts []string
		for _, it := range seqOf(arg(0)) {
			parts = append(parts, stringOf(it))
		}
		return strings.Join(parts, sep), nil
	case "index-of":
		target := str(arg(1))
		var out []any
		for i, it := range seqOf(arg(0)) {
			if stringOf(it) == target {
				out = append(out, rat(int64(i+1)))
			}
		}
		return out, nil
	case "last":
		return rat(int64(c.size)), nil
	case "position":
		return rat(int64(c.pos)), nil
	case "name", "node-name", "prefixed-name":
		return nameOf(focus(0), true), nil
	case "local-name":
		return nameOf(focus(0), false), nil
	case "prefix-from-QName":
		return nameOf(arg(0), false), nil
	case "namespace-uri-for-prefix", "resolve-uri", "doc", "document", "doc-available",
		"unparsed-text", "format-number", "current", "root", "generate-id", "compare", "contains-token":
		return nil, errf("function %s not supported", f.name)
	}
	if xs {
		// Unknown XML Schema builtin: treat as a string cast.
		return mapStr(arg(0), func(s string) any { return strings.TrimSpace(s) }), nil
	}
	return nil, errf("unknown function %s", f.name)
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
