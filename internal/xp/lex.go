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

func lex(src string) ([]token, error) {
	var out []token
	i := 0
	isNameStart := func(c byte) bool {
		return c == '_' || c == '*' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || c >= 0x80
	}
	for i < len(src) {
		c := src[i]
		switch {
		case c == ' ' || c == '\t' || c == '\n' || c == '\r':
			i++
			continue
		}
		if (c >= '0' && c <= '9') || (c == '.' && i+1 < len(src) && src[i+1] >= '0' && src[i+1] <= '9') {
			j := i
			for j < len(src) && ((src[j] >= '0' && src[j] <= '9') || src[j] == '.' || src[j] == 'e' || src[j] == 'E' ||
				((src[j] == '+' || src[j] == '-') && j > i && (src[j-1] == 'e' || src[j-1] == 'E'))) {
				j++
			}
			out = append(out, token{k: tNum, num: src[i:j], pos: i})
			i = j
			continue
		}
		if c == '"' || c == '\'' {
			q := c
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
				return nil, fmt.Errorf("xp: unterminated string at %d", i)
			}
			out = append(out, token{k: tStr, s: b.String(), pos: i})
			i = j + 1
			continue
		}
		if c == '$' {
			j := i + 1
			for j < len(src) && (src[j] == '-' || src[j] == '_' || src[j] == ':' || src[j] == '.' ||
				(src[j] >= '0' && src[j] <= '9') || (src[j] >= 'a' && src[j] <= 'z') || (src[j] >= 'A' && src[j] <= 'Z')) {
				j++
			}
			out = append(out, token{k: tVar, s: src[i+1 : j], pos: i})
			i = j
			continue
		}
		switch {
		case c == '/' && i+1 < len(src) && src[i+1] == '/':
			out = append(out, token{k: tDSlash, pos: i})
			i += 2
			continue
		case c == '/':
			out = append(out, token{k: tSlash, pos: i})
			i++
			continue
		case c == '.' && i+1 < len(src) && src[i+1] == '.':
			out = append(out, token{k: tDotDot, pos: i})
			i += 2
			continue
		case c == '.' && i+1 < len(src) && src[i+1] == ':' && i+2 < len(src) && src[i+2] == ':':
			out = append(out, token{k: tName, s: "child", pos: i})
			out = append(out, token{k: tColonColon, pos: i})
			i += 3
			continue
		case c == '.':
			out = append(out, token{k: tDot, pos: i})
			i++
			continue
		case c == '@':
			out = append(out, token{k: tAt, pos: i})
			i++
			continue
		case c == ':' && i+1 < len(src) && src[i+1] == '=':
			// let $x := e
			out = append(out, token{k: tOp, s: ":=", pos: i})
			i += 2
			continue
		case c == ':':
			if i+1 < len(src) && src[i+1] == ':' {
				out = append(out, token{k: tColonColon, pos: i})
				i += 2
				continue
			}
			return nil, fmt.Errorf("xp: unexpected ':' at %d", i)
		case c == '(':
			out = append(out, token{k: tLP, pos: i})
			i++
			continue
		case c == ')':
			out = append(out, token{k: tRP, pos: i})
			i++
			continue
		case c == '[':
			out = append(out, token{k: tLB, pos: i})
			i++
			continue
		case c == ']':
			out = append(out, token{k: tRB, pos: i})
			i++
			continue
		case c == ',':
			out = append(out, token{k: tComma, pos: i})
			i++
			continue
		case c == '|':
			out = append(out, token{k: tUnion, pos: i})
			i++
			continue
		case c == '=' && i+1 < len(src) && src[i+1] == '>':
			out = append(out, token{k: tOp, s: "=>", pos: i})
			i += 2
			continue
		case c == '!':
			if i+1 < len(src) && src[i+1] == '=' {
				out = append(out, token{k: tOp, s: "!=", pos: i})
				i += 2
				continue
			}
			out = append(out, token{k: tOp, s: "!", pos: i})
			i++
			continue
		case c == '<':
			if i+1 < len(src) && src[i+1] == '=' {
				out = append(out, token{k: tOp, s: "<=", pos: i})
				i += 2
				continue
			}
			out = append(out, token{k: tOp, s: "<", pos: i})
			i++
			continue
		case c == '>':
			if i+1 < len(src) && src[i+1] == '=' {
				out = append(out, token{k: tOp, s: ">=", pos: i})
				i += 2
				continue
			}
			out = append(out, token{k: tOp, s: ">", pos: i})
			i++
			continue
		case c == '=':
			out = append(out, token{k: tOp, s: "=", pos: i})
			i++
			continue
		case c == '+' || c == '-':
			out = append(out, token{k: tOp, s: string(c), pos: i})
			i++
			continue
		}
		if c == '*' {
			out = append(out, token{k: tName, s: "*", pos: i})
			i++
			continue
		}
		if isNameStart(c) {
			j := i
			for j < len(src) {
				ch := src[j]
				if ch == '-' || ch == '_' || ch == '.' || (ch >= '0' && ch <= '9') ||
					(ch >= 'a' && ch <= 'z') || (ch >= 'A' && ch <= 'Z') || ch >= 0x80 {
					j++
					continue
				}
				if ch == ':' && j+1 < len(src) && (src[j+1] == '*' || src[j+1] == '_' ||
					(src[j+1] >= 'a' && src[j+1] <= 'z') || (src[j+1] >= 'A' && src[j+1] <= 'Z')) {
					j += 2
					continue
				}
				break
			}
			w := src[i:j]
			if wordOps[w] {
				out = append(out, token{k: tOp, s: w, pos: i})
			} else {
				out = append(out, token{k: tName, s: w, pos: i})
			}
			i = j
			continue
		}
		return nil, fmt.Errorf("xp: unexpected character %q at %d in %q", c, i, src)
	}
	out = append(out, token{k: tEOF, pos: len(src)})
	return out, nil
}
