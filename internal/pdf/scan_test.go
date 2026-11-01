package pdf

import "testing"

// The literal string grammar hides several traps: balanced parentheses, the
// octal escapes that take one to three digits, and the backslash at the end of
// a line that swallows the break. These pin the behaviour the refactor of
// literalString must preserve.

func TestLiteralStringEscapes(t *testing.T) {
	for _, tc := range []struct {
		name string
		in   string
		want string
	}{
		{"plain", `(hello)`, "hello"},
		{"simple escapes", `(a\nb\rc\td\be\f)`, "a\nb\rc\td\be\f"},
		{"paired escapes", `(\(\)\\)`, "()\\"},
		{"nested parens", `((a)b(c))`, "(a)b(c)"},
		{"unbalanced open", `((a))`, "(a)"},
		{"three octal digits", `(\101\10\1)`, "A\x08\x01"},
		{"octal stops at three digits", `(\1010)`, "A0"},
		{"octal max byte", `(\377)`, "\xff"},
		{"crlf continuation", `(a\` + "\r\n" + `b)`, "ab"},
		{"lf continuation", `(a\` + "\n" + `b)`, "ab"},
		{"unknown escape is literal", `(a\qb)`, "aqb"},
		{"empty", `()`, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := &scanner{data: []byte(tc.in)}
			got, err := r.literalString()
			if err != nil {
				t.Fatalf("literalString(%q) = %v", tc.in, err)
			}
			s, ok := got.(String)
			if !ok {
				t.Fatalf("literalString(%q) returned %T, want String", tc.in, got)
			}
			if string(s) != tc.want {
				t.Errorf("literalString(%q) = %q, want %q", tc.in, string(s), tc.want)
			}
			if r.pos != len(r.data) {
				t.Errorf("pos = %d after %q, want the token consumed", r.pos, tc.in)
			}
		})
	}
}

func TestLiteralStringErrors(t *testing.T) {
	for _, tc := range []struct {
		name string
		in   string
	}{
		{"unterminated", `(abc`},
		{"escape at end", `(abc\`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := &scanner{data: []byte(tc.in)}
			if _, err := r.literalString(); err == nil {
				t.Errorf("literalString(%q) succeeded, want an error", tc.in)
			}
		})
	}
}
