package xp

import (
	"strings"
	"testing"
)

// tokNames renders a token stream as a single line so the scanner tests can
// compare the whole stream at once.
func tokNames(toks []token) string {
	names := map[tokKind]string{
		tEOF: "eof", tName: "name", tNum: "num", tStr: "str", tOp: "op",
		tLP: "'('", tRP: "')'", tLB: "'['", tRB: "']'", tComma: "','",
		tVar: "var", tSlash: "'/'", tDSlash: "'//'", tDot: "'.'",
		tDotDot: "'..'", tAt: "'@'", tColonColon: "'::'", tUnion: "'|'",
	}
	parts := make([]string, 0, len(toks))
	for _, tk := range toks {
		k := names[tk.k]
		if k == "" {
			k = "?"
		}
		switch {
		case tk.s != "":
			parts = append(parts, k+"="+tk.s)
		case tk.num != "":
			parts = append(parts, k+"="+tk.num)
		default:
			parts = append(parts, k)
		}
	}
	return strings.Join(parts, " ")
}

// TestLexCorners pins the token stream for the awkward corners of the scanner:
// the "." family, the ".::" shorthand, the ":=" of a let clause, a doubled
// quote inside a string literal, a scientific-notation number and the arrow
// operator, which the scanner recognises even though the grammar does not.
func TestLexCorners(t *testing.T) {
	cases := []struct{ src, want string }{
		{`..`, `'..' eof`},
		{`.5`, `num=.5 eof`},
		{`1.5e3`, `num=1.5e3 eof`},
		{`.::ram:A`, `name=child '::' name=ram:A eof`},
		{`ancestor::ram:A`, `name=ancestor '::' name=ram:A eof`},
		{`$total := 3`, `var=total op=:= num=3 eof`},
		{`count(1) => string()`, `name=count '(' num=1 ')' op==> name=string '(' ')' eof`},
		{`"it's"`, `str=it's eof`},
		{`""""`, `str=" eof`},
		{`*=2`, `name=* op== num=2 eof`},
	}
	for _, c := range cases {
		toks, err := lex(c.src)
		if err != nil {
			t.Errorf("lex %s: %v", c.src, err)
			continue
		}
		if got := tokNames(toks); got != c.want {
			t.Errorf("lex %s = %s, want %s", c.src, got, c.want)
		}
	}
}

// TestLexErrors checks that a malformed token is reported as a lexer error
// rather than reaching the parser as junk.
func TestLexErrors(t *testing.T) {
	for _, src := range []string{
		`"abc`,     // unterminated literal
		`'abc`,     // unterminated literal, other quote
		`1 + :`,    // a colon that belongs to no name
		`1 + %`,    // not a character XPath uses
		`foo("bar`, // unterminated argument
	} {
		if _, err := Compile(src, tns); err == nil {
			t.Errorf("compile %s succeeded, want a lexer error", src)
		}
	}
}
