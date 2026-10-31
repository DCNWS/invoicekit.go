package pdf

import (
	"fmt"
	"strconv"
)

// scanner is a lexical reader over PDF content.
type scanner struct {
	data []byte
	pos  int
}

func isSpaceByte(c byte) bool {
	switch c {
	case 0x00, 0x09, 0x0a, 0x0c, 0x0d, 0x20:
		return true
	}
	return false
}

func isDelim(c byte) bool {
	switch c {
	case '(', ')', '<', '>', '[', ']', '{', '}', '/', '%':
		return true
	}
	return isSpaceByte(c)
}

// skipToToken advances past whitespace and comments.
func (r *scanner) skipToToken() {
	for r.pos < len(r.data) {
		c := r.data[r.pos]
		if c == '%' {
			for r.pos < len(r.data) && r.data[r.pos] != '\n' && r.data[r.pos] != '\r' {
				r.pos++
			}
			continue
		}
		if isSpaceByte(c) {
			r.pos++
			continue
		}
		return
	}
}

// keyword reports whether the token at the current position is the given
// keyword, without consuming it.
func (r *scanner) keyword(word string) bool {
	r.skipToToken()
	if r.pos+len(word) > len(r.data) {
		return false
	}
	for i := 0; i < len(word); i++ {
		if r.data[r.pos+i] != word[i] {
			return false
		}
	}
	next := r.pos + len(word)
	return next >= len(r.data) || isDelim(r.data[next])
}

// intNumber reads an integer token.
func (r *scanner) intNumber() (int64, error) {
	r.skipToToken()
	start := r.pos
	for r.pos < len(r.data) && !isDelim(r.data[r.pos]) {
		r.pos++
	}
	tok := string(r.data[start:r.pos])
	if tok == "" {
		return 0, fmt.Errorf("pdf: expected a number at offset %d", start)
	}
	v, err := strconv.ParseInt(tok, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("pdf: expected an integer, got %q at offset %d", tok, start)
	}
	return v, nil
}

// number reads an integer or real token.
func (r *scanner) number() (Object, error) {
	r.skipToToken()
	start := r.pos
	for r.pos < len(r.data) && !isDelim(r.data[r.pos]) {
		r.pos++
	}
	tok := string(r.data[start:r.pos])
	return parseNumberToken(tok, start)
}

func parseNumberToken(tok string, at int) (Object, error) {
	if tok == "" {
		return nil, fmt.Errorf("pdf: expected a number at offset %d", at)
	}
	if hasDot(tok) {
		v, err := strconv.ParseFloat(tok, 64)
		if err != nil {
			return nil, fmt.Errorf("pdf: bad real number %q at offset %d", tok, at)
		}
		return Real(v), nil
	}
	v, err := strconv.ParseInt(tok, 10, 64)
	if err != nil {
		vf, ferr := strconv.ParseFloat(tok, 64)
		if ferr != nil {
			return nil, fmt.Errorf("pdf: bad number %q at offset %d", tok, at)
		}
		return Real(vf), nil
	}
	return Int(v), nil
}

func hasDot(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] == '.' {
			return true
		}
	}
	return false
}

// objectHeader reads "num gen obj".
func (r *scanner) objectHeader() (int, int, error) {
	num, err := r.intNumber()
	if err != nil {
		return 0, 0, err
	}
	gen, err := r.intNumber()
	if err != nil {
		return 0, 0, err
	}
	if !r.keyword("obj") {
		return 0, 0, fmt.Errorf("pdf: expected obj keyword after %d %d", num, gen)
	}
	r.pos += len("obj")
	return int(num), int(gen), nil
}

// value parses any PDF object value.
func (r *scanner) value(f *File) (Object, error) {
	r.skipToToken()
	if r.pos >= len(r.data) {
		return nil, fmt.Errorf("pdf: unexpected end of data")
	}
	switch c := r.data[r.pos]; {
	case c == '/':
		return r.name()
	case c == '[':
		return r.array(f)
	case c == '(':
		return r.literalString()
	case c == '<':
		if r.pos+1 < len(r.data) && r.data[r.pos+1] == '<' {
			return r.dict(f)
		}
		return r.hexString()
	case c >= '0' && c <= '9', c == '+', c == '-', c == '.':
		return r.referenceOrNumber(f)
	case c == 't':
		if r.keyword("true") {
			r.pos += len("true")
			return Bool(true), nil
		}
	case c == 'f':
		if r.keyword("false") {
			r.pos += len("false")
			return Bool(false), nil
		}
	case c == 'n':
		if r.keyword("null") {
			r.pos += len("null")
			return Null{}, nil
		}
	}
	return nil, fmt.Errorf("pdf: unexpected token %q at offset %d", r.tokenText(), r.pos)
}

func (r *scanner) tokenText() string {
	start := r.pos
	for r.pos < len(r.data) && !isDelim(r.data[r.pos]) {
		r.pos++
	}
	t := string(r.data[start:r.pos])
	r.pos = start
	if t == "" && r.pos < len(r.data) {
		return string(r.data[r.pos])
	}
	return t
}

// referenceOrNumber parses "12 0 R" or a plain number.
func (r *scanner) referenceOrNumber(f *File) (Object, error) {
	save := r.pos
	num, err := r.intNumber()
	if err == nil {
		save2 := r.pos
		gen, err2 := r.intNumber()
		if err2 == nil && r.keyword("R") {
			r.pos += len("R")
			return Ref{Num: int(num), Gen: int(gen)}, nil
		}
		_ = save2
	}
	r.pos = save
	return r.number()
}

// name parses a PDF name.
func (r *scanner) name() (Object, error) {
	r.pos++ // slash
	var b []byte
	for r.pos < len(r.data) {
		c := r.data[r.pos]
		if isDelim(c) {
			break
		}
		if c == '#' && r.pos+2 < len(r.data) {
			hi, e1 := hexValue(r.data[r.pos+1])
			lo, e2 := hexValue(r.data[r.pos+2])
			if e1 && e2 {
				b = append(b, hi<<4|lo)
				r.pos += 3
				continue
			}
		}
		b = append(b, c)
		r.pos++
	}
	return Name(string(b)), nil
}

func hexValue(c byte) (byte, bool) {
	switch {
	case c >= '0' && c <= '9':
		return c - '0', true
	case c >= 'a' && c <= 'f':
		return c - 'a' + 10, true
	case c >= 'A' && c <= 'F':
		return c - 'A' + 10, true
	}
	return 0, false
}

// array parses "[ ... ]".
func (r *scanner) array(f *File) (Object, error) {
	r.pos++
	a := Array{}
	for {
		r.skipToToken()
		if r.pos >= len(r.data) {
			return nil, fmt.Errorf("pdf: unterminated array")
		}
		if r.data[r.pos] == ']' {
			r.pos++
			return a, nil
		}
		v, err := r.value(f)
		if err != nil {
			return nil, err
		}
		a = append(a, v)
	}
}

// dict parses "<< ... >>".
func (r *scanner) dict(f *File) (Object, error) {
	r.pos += 2
	d := NewDict()
	for {
		r.skipToToken()
		if r.pos >= len(r.data) {
			return nil, fmt.Errorf("pdf: unterminated dictionary")
		}
		if r.data[r.pos] == '>' && r.pos+1 < len(r.data) && r.data[r.pos+1] == '>' {
			r.pos += 2
			return d, nil
		}
		if r.data[r.pos] != '/' {
			return nil, fmt.Errorf("pdf: expected a name key in dictionary at offset %d, got %q", r.pos, r.tokenText())
		}
		n, err := r.name()
		if err != nil {
			return nil, err
		}
		v, err := r.value(f)
		if err != nil {
			return nil, err
		}
		d.Set(string(n.(Name)), v)
	}
}

// literalString parses "( ... )" with the escapes of ISO 32000.
func (r *scanner) literalString() (Object, error) {
	r.pos++
	depth := 1
	var b []byte
	for r.pos < len(r.data) {
		c := r.data[r.pos]
		r.pos++
		switch c {
		case '(':
			depth++
			b = append(b, c)
		case ')':
			depth--
			if depth == 0 {
				return String(string(b)), nil
			}
			b = append(b, c)
		case '\\':
			if r.pos >= len(r.data) {
				return nil, fmt.Errorf("pdf: unterminated string escape")
			}
			e := r.data[r.pos]
			r.pos++
			switch e {
			case 'n':
				b = append(b, '\n')
			case 'r':
				b = append(b, '\r')
			case 't':
				b = append(b, '\t')
			case 'b':
				b = append(b, '\b')
			case 'f':
				b = append(b, '\f')
			case '(':
				b = append(b, '(')
			case ')':
				b = append(b, ')')
			case '\\':
				b = append(b, '\\')
			case '\r':
				if r.pos < len(r.data) && r.data[r.pos] == '\n' {
					r.pos++
				}
			case '\n':
				// line continuation
			default:
				if e >= '0' && e <= '7' {
					oct := []byte{e}
					for len(oct) < 3 && r.pos < len(r.data) && r.data[r.pos] >= '0' && r.data[r.pos] <= '7' {
						oct = append(oct, r.data[r.pos])
						r.pos++
					}
					v, _ := strconv.ParseInt(string(oct), 8, 32)
					b = append(b, byte(v))
				} else {
					b = append(b, e)
				}
			}
		default:
			b = append(b, c)
		}
	}
	return nil, fmt.Errorf("pdf: unterminated literal string")
}

// hexString parses "< ... >".
func (r *scanner) hexString() (Object, error) {
	r.pos++
	var nib []byte
	for r.pos < len(r.data) {
		c := r.data[r.pos]
		r.pos++
		if c == '>' {
			if len(nib)%2 == 1 {
				nib = append(nib, '0')
			}
			b := make([]byte, len(nib)/2)
			for i := 0; i < len(b); i++ {
				hi, _ := hexValue(nib[2*i])
				lo, _ := hexValue(nib[2*i+1])
				b[i] = hi<<4 | lo
			}
			return String(string(b)), nil
		}
		if isSpaceByte(c) {
			continue
		}
		if _, ok := hexValue(c); !ok {
			return nil, fmt.Errorf("pdf: bad hex digit %q in hexadecimal string", c)
		}
		nib = append(nib, c)
	}
	return nil, fmt.Errorf("pdf: unterminated hexadecimal string")
}
