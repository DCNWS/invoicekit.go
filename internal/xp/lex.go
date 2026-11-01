package xp

import (
	"fmt"
	"strings"
)

type tokKind int

const (
	tEOF tokKind = iota
	tName
	tNum
	tStr
	tOp
	tLP
	tRP
	tLB
	tRB
	tComma
	tVar
	tSlash
	tDSlash
	tDot
	tDotDot
	tAt
	tColonColon
	tUnion
)

type token struct {
	k   tokKind
	s   string
	num string
	pos int
}

var wordOps = map[string]bool{
	"and": true, "or": true, "div": true, "mod": true, "idiv": true,
	"satisfies": true, "in": true, "then": true, "else": true, "if": true,
	"some": true, "every": true, "return": true, "let": true, "for": true,
	"eq": true, "ne": true, "lt": true, "le": true, "gt": true, "ge": true,
	"is": true, "cast": true, "castable": true, "as": true, "to": true,
}

// lex splits an XPath expression into tokens. One loop walks the input; each
// token form is a small function of its own, so the scanner reads as a list of
// what the grammar allows rather than 200 lines of index arithmetic.
func lex(src string) ([]token, error) {
	var out []token
	for i := 0; i < len(src); {
		if c := src[i]; c == ' ' || c == '\t' || c == '\n' || c == '\r' {
			i++
			continue
		}
		ts, n, err := lexToken(src, i)
		if err != nil {
			return nil, err
		}
		out = append(out, ts...)
		i += n
	}
	out = append(out, token{k: tEOF, pos: len(src)})
	return out, nil
}

// lexToken reads the token starting at src[i] and returns it along with the
// number of bytes it consumed. A step of two tokens is possible: ".::" is the
// shorthand for the child axis and expands to a name and a "::".
func lexToken(src string, i int) ([]token, int, error) {
	c := src[i]
	switch {
	case isDigit(c) || (c == '.' && isDigit(at(src, i+1))):
		return lexNumber(src, i)
	case c == '"' || c == '\'':
		return lexString(src, i)
	case c == '$':
		return lexVar(src, i)
	}
	if ts, n, ok := lexPunct(src, i); ok {
		return ts, n, nil
	}
	if c == '*' {
		// "*" is a name, and it never continues into one: "*:x" is three
		// tokens, which the name scanner below would otherwise read as one.
		return []token{{k: tName, s: "*", pos: i}}, 1, nil
	}
	if isNameStart(c) {
		return lexName(src, i)
	}
	if c == ':' {
		return nil, 0, fmt.Errorf("xp: unexpected ':' at %d", i)
	}
	return nil, 0, fmt.Errorf("xp: unexpected character %q at %d in %q", c, i, src)
}

// at is src[j], or 0 past the end. No XPath token contains a NUL, so the
// sentinel reads as end of input wherever a caller needs to peek one byte.
func at(src string, j int) byte {
	if j >= len(src) {
		return 0
	}
	return src[j]
}

func isDigit(c byte) bool { return c >= '0' && c <= '9' }

func isNameStart(c byte) bool {
	return c == '_' || c == '*' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || c >= 0x80
}

func isNameChar(c byte) bool {
	return c == '-' || c == '_' || c == '.' || isDigit(c) ||
		(c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || c >= 0x80
}

// singlePunct are the tokens spelled with exactly one character.
var singlePunct = map[byte]tokKind{
	'(': tLP, ')': tRP, '[': tLB, ']': tRB, ',': tComma, '|': tUnion, '@': tAt,
}

// lexPunct reads operators, axes and delimiters. Returns ok false when src[i]
// does not start one.
func lexPunct(src string, i int) ([]token, int, bool) {
	switch c := src[i]; c {
	case '/':
		if at(src, i+1) == '/' {
			return []token{{k: tDSlash, pos: i}}, 2, true
		}
		return []token{{k: tSlash, pos: i}}, 1, true
	case '.':
		return lexDot(src, i)
	case ':':
		return lexColon(src, i)
	case '!', '<', '>', '=', '+', '-':
		return lexCmp(src, i)
	default:
		if k, ok := singlePunct[c]; ok {
			return []token{{k: k, pos: i}}, 1, true
		}
		return nil, 0, false
	}
}

// lexDot reads ".", ".." and the ".::" shorthand for the child axis. A "."
// before a digit never reaches here: that is the start of a number.
func lexDot(src string, i int) ([]token, int, bool) {
	switch {
	case at(src, i+1) == '.':
		return []token{{k: tDotDot, pos: i}}, 2, true
	case at(src, i+1) == ':' && at(src, i+2) == ':':
		return []token{{k: tName, s: "child", pos: i}, {k: tColonColon, pos: i}}, 3, true
	}
	return []token{{k: tDot, pos: i}}, 1, true
}

// lexColon reads "::" and the ":=" of a let clause. A lone ":" is not a token;
// it separates a prefix from a local name, and the name scanner only accepts
// it when a name follows.
func lexColon(src string, i int) ([]token, int, bool) {
	switch at(src, i+1) {
	case ':':
		return []token{{k: tColonColon, pos: i}}, 2, true
	case '=':
		return []token{{k: tOp, s: ":=", pos: i}}, 2, true
	}
	return nil, 0, false
}

// lexCmp reads the comparison operators and arithmetic signs. The value
// operators are spelled out ("eq", "lt") in XPath, so a bare "=" is always one
// token, never the start of "==".
func lexCmp(src string, i int) ([]token, int, bool) {
	c := src[i]
	if c == '=' && at(src, i+1) == '>' {
		return []token{{k: tOp, s: "=>", pos: i}}, 2, true // the arrow operator
	}
	if c == '=' {
		return []token{{k: tOp, s: "=", pos: i}}, 1, true
	}
	s := string(c)
	if c != '+' && c != '-' && at(src, i+1) == '=' {
		s += "="
	}
	return []token{{k: tOp, s: s, pos: i}}, len(s), true
}

func lexNumber(src string, i int) ([]token, int, error) {
	j := i
	for j < len(src) && (isDigit(src[j]) || src[j] == '.' || src[j] == 'e' || src[j] == 'E' ||
		((src[j] == '+' || src[j] == '-') && j > i && (src[j-1] == 'e' || src[j-1] == 'E'))) {
		j++
	}
	return []token{{k: tNum, num: src[i:j], pos: i}}, j - i, nil
}

func lexString(src string, i int) ([]token, int, error) {
	q := src[i]
	j := i + 1
	var b strings.Builder
	// XPath string literals have no backslash escapes; the only way to
	// include the delimiter is to double it.
	for j < len(src) {
		if src[j] == q {
			if j+1 < len(src) && src[j+1] == q {
				b.WriteByte(q)
				j += 2
				continue
			}
			break
		}
		b.WriteByte(src[j])
		j++
	}
	if j >= len(src) {
		return nil, 0, fmt.Errorf("xp: unterminated string at %d", i)
	}
	return []token{{k: tStr, s: b.String(), pos: i}}, j + 1 - i, nil
}

func lexVar(src string, i int) ([]token, int, error) {
	j := i + 1
	for j < len(src) && (src[j] == '-' || src[j] == '_' || src[j] == ':' || src[j] == '.' ||
		isDigit(src[j]) || (src[j] >= 'a' && src[j] <= 'z') || (src[j] >= 'A' && src[j] <= 'Z')) {
		j++
	}
	return []token{{k: tVar, s: src[i+1 : j], pos: i}}, j - i, nil
}

// lexName reads an NCName or an expanded name such as ram:InvoiceLine. The
// colon is part of the name only when a name follows it, which is what keeps
// "ram:*" and "ns::" lexing the way the grammar says they should.
func lexName(src string, i int) ([]token, int, error) {
	j := i
	for j < len(src) {
		ch := src[j]
		if isNameChar(ch) {
			j++
			continue
		}
		if ch == ':' && (at(src, j+1) == '*' || isNameStartASCII(src[j+1])) {
			j += 2
			continue
		}
		break
	}
	w := src[i:j]
	if wordOps[w] {
		return []token{{k: tOp, s: w, pos: i}}, j - i, nil
	}
	return []token{{k: tName, s: w, pos: i}}, j - i, nil
}

func isNameStartASCII(c byte) bool {
	return c == '_' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
}
