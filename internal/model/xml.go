package model

import (
	"fmt"
	"strings"
)

// attr is one attribute of an element. Empty values are dropped, which is how
// optional attributes are handled: the caller passes attr{"schemeID", s} and
// the attribute simply does not appear when s is empty.
type attr struct{ k, v string }

// xw is an ordered XML writer.
//
// Both syntaxes are defined by sequence-based schemas: for a CII or UBL
// element, the order of its children is part of its validity, and so is the
// exact set of children. A reflective marshaller over structs cannot express
// that, so every renderer writes the document element by element in the order
// the schema prescribes. This type only provides the mechanics; the order
// lives in cii.go and ubl.go.
//
// A renderer never panics and never returns a partial document as a success:
// a missing mandatory value is recorded with bad and reported as an error at
// the end, so callers get the full list of what the invoice lacks.
type xw struct {
	b    strings.Builder
	ind  int
	path []string
	errs []string
}

// doc returns the document, prefixed with the XML declaration.
func (x *xw) doc() string {
	return `<?xml version="1.0" encoding="UTF-8"?>` + "\n" + x.b.String() + "\n"
}

func (x *xw) nl() {
	x.b.WriteString("\n")
	x.b.WriteString(strings.Repeat("  ", x.ind))
}

func (x *xw) attrs(a []attr) {
	for _, at := range a {
		if at.v == "" {
			continue
		}
		x.b.WriteString(" " + at.k + `="` + escape(at.v) + `"`)
	}
}

// start opens a container element.
func (x *xw) start(name string, a ...attr) {
	x.nl()
	x.b.WriteString("<" + name)
	x.attrs(a)
	x.b.WriteString(">")
	x.path = append(x.path, name)
	x.ind++
}

// end closes a container element.
func (x *xw) end(name string) {
	if n := len(x.path); n == 0 || x.path[n-1] != name {
		x.bad("internal: closing " + name + " with no matching open element")
		return
	}
	x.path = x.path[:len(x.path)-1]
	x.ind--
	x.nl()
	x.b.WriteString("</" + name + ">")
}

// elem writes a leaf element with text content.
func (x *xw) elem(name, v string, a ...attr) {
	x.nl()
	x.b.WriteString("<" + name)
	x.attrs(a)
	x.b.WriteString(">" + escape(v) + "</" + name + ">")
}

// opt writes a leaf element unless the value is empty.
func (x *xw) opt(name, v string, a ...attr) {
	if v == "" {
		return
	}
	x.elem(name, v, a...)
}

// mand writes a leaf element the syntax requires. An empty value is written as
// empty content rather than skipped: the CII and UBL types are built on
// xs:token and xs:decimal, which accept it, so the document stays schematically
// valid and the validator reports the missing term as a business rule finding.
// A renderer that refused to emit the element would produce an invalid
// document and hide every other defect in it.
func (x *xw) mand(name, v string, a ...attr) { x.elem(name, v, a...) }

// money writes a mandatory amount.
func (x *xw) money(name string, v float64) { x.elem(name, Money(v)) }

// pmoney writes a mandatory amount held in a pointer.
func (x *xw) pmoney(name string, v *float64) {
	if v == nil {
		x.bad(name + " is missing")
		return
	}
	x.elem(name, Money(*v))
}

// bad records a defect in the invoice that prevents rendering.
func (x *xw) bad(msg string) {
	x.errs = append(x.errs, where(x.path)+": "+msg)
}

func (x *xw) badf(format string, a ...any) {
	x.errs = append(x.errs, where(x.path)+": "+fmt.Sprintf(format, a...))
}

// errf renders an error from the recorded defects.
func (x *xw) errf(prefix string) error {
	return fmt.Errorf("%s: cannot render: %s", prefix, strings.Join(x.errs, "; "))
}

// where names the element a defect was found in, for readable errors.
func where(path []string) string {
	if len(path) == 0 {
		return "document"
	}
	return strings.Join(path, "/")
}

// escape writes text content. Control characters that XML does not allow at
// all are dropped rather than encoded, because encoding them would produce a
// document an XML parser rejects.
func escape(s string) string {
	if !strings.ContainsAny(s, `<>&"'`+"\x00\x01\x02\x03\x04\x05\x06\a\b\v\f\x0e\x0f\x10\x11\x12\x13\x14\x15\x16\x17\x18\x19\x1a\x1b\x1c\x1d\x1e\x1f") {
		return s
	}
	var b strings.Builder
	for _, r := range s {
		switch r {
		case '&':
			b.WriteString("&amp;")
		case '<':
			b.WriteString("&lt;")
		case '>':
			b.WriteString("&gt;")
		case '"':
			b.WriteString("&quot;")
		case '\'':
			b.WriteString("&apos;")
		case '\t', '\n', '\r':
			b.WriteRune(r)
		default:
			if r < 0x20 || r == 0x7f {
				continue
			}
			b.WriteRune(r)
		}
	}
	return b.String()
}

// isoDate normalises a date to the YYYY-MM-DD form both syntaxes use for
// dates. Values already in that form are returned unchanged; an unparseable
// value is returned unchanged as well and reported by the caller's checks.
func isoDate(s string) string {
	s = strings.TrimSpace(s)
	if len(s) == 10 && s[4] == '-' && s[7] == '-' {
		return s
	}
	if len(s) == 8 {
		return s[:4] + "-" + s[4:6] + "-" + s[6:]
	}
	return s
}
