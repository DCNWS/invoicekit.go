// Package pdf implements the subset of ISO 32000 (PDF) needed to read an
// existing document, modify its catalog-level structures and write it back
// out. It exists to embed Factur-X / ZUGFeRD cross-industry invoice XML into
// a PDF/A-3 container without shelling out to a third-party PDF tool.
//
// The model is deliberately small: objects are represented by Go values,
// streams keep their encoded bytes so that no filter support beyond
// FlateDecode is required for round-tripping, and the cross-reference
// structures are rebuilt as a classic table on output so that the result is
// readable by every consumer, including the strictest PDF/A validators.
//
// Encrypted documents are refused outright: PDF/A forbids them anyway.
package pdf

import (
	"errors"
	"fmt"
	"sort"
	"strconv"
)

// Object is any PDF value.
type Object interface{}

// Null is the PDF null object.
type Null struct{}

// Bool is a PDF boolean.
type Bool bool

// Int is an integer in the range allowed by ISO 32000 (-2^31+1 .. 2^31-1).
type Int int64

// Real is a PDF real number. The value is kept as a float64; output uses
// enough digits to round-trip.
type Real float64

// Name is a PDF name without the leading slash, in its decoded form.
type Name string

// String is a PDF byte string (as opposed to a Go string, it may hold
// arbitrary bytes, including UTF-16 with a byte order mark).
type String string

// Array is a PDF array.
type Array []Object

// Ref is an indirect reference to object number Num, generation Gen.
type Ref struct {
	Num int
	Gen int
}

func (r Ref) String() string { return fmt.Sprintf("%d %d R", r.Num, r.Gen) }

// Dict is a PDF dictionary that preserves the order in which keys were
// inserted, so that rewriting a document produces predictable output.
type Dict struct {
	keys []string
	vals map[string]Object
}

// NewDict returns an empty dictionary.
func NewDict() *Dict { return &Dict{vals: map[string]Object{}} }

// DictFrom returns a dictionary holding the given key/value pairs, in order.
// Values may be any Object, including plain Go ints and strings, which are
// normalised by Get-style accessors on write.
func DictFrom(kv ...Object) *Dict {
	d := NewDict()
	for i := 0; i+1 < len(kv); i += 2 {
		k, ok := kv[i].(Name)
		if !ok {
			panic(fmt.Sprintf("pdf: dictionary key must be Name, got %T", kv[i]))
		}
		d.Set(string(k), kv[i+1])
	}
	return d
}

// Get returns the raw value stored under key, or nil.
func (d *Dict) Get(key string) Object {
	if d == nil {
		return nil
	}
	return d.vals[key]
}

// Has reports whether key is present.
func (d *Dict) Has(key string) bool {
	if d == nil {
		return false
	}
	_, ok := d.vals[key]
	return ok
}

// Set stores value under key, appending the key if it is new.
func (d *Dict) Set(key string, value Object) {
	if d.vals == nil {
		d.vals = map[string]Object{}
	}
	if _, ok := d.vals[key]; !ok {
		d.keys = append(d.keys, key)
	}
	d.vals[key] = value
}

// Delete removes key.
func (d *Dict) Delete(key string) {
	if _, ok := d.vals[key]; !ok {
		return
	}
	delete(d.vals, key)
	for i, k := range d.keys {
		if k == key {
			d.keys = append(d.keys[:i], d.keys[i+1:]...)
			break
		}
	}
}

// Keys returns the keys in insertion order.
func (d *Dict) Keys() []string {
	if d == nil {
		return nil
	}
	return append([]string(nil), d.keys...)
}

// SortedKeys returns the keys in dictionary order, which PDF requires for
// some structures such as name trees.
func (d *Dict) SortedKeys() []string {
	ks := d.Keys()
	sort.Strings(ks)
	return ks
}

// Len returns the number of entries.
func (d *Dict) Len() int {
	if d == nil {
		return 0
	}
	return len(d.vals)
}

// Clone returns a shallow copy.
func (d *Dict) Clone() *Dict {
	n := NewDict()
	for _, k := range d.keys {
		n.Set(k, d.vals[k])
	}
	return n
}

// Stream is a PDF stream. Raw holds the bytes exactly as they appear between
// the stream and endstream keywords, i.e. still encoded by whatever filter the
// dictionary names. Keeping them avoids having to implement every filter just
// to copy a document around.
type Stream struct {
	Dict *Dict
	Raw  []byte
}

// NewStream returns a stream whose dictionary carries the given key/value
// pairs (plus a corrected /Length).
func NewStream(data []byte, kv ...Object) *Stream {
	s := &Stream{Dict: DictFrom(kv...), Raw: data}
	return s
}

// TypeName returns the value of /Type as a decoded name.
func (d *Dict) TypeName() string {
	n, _ := d.Get("Type").(Name)
	return string(n)
}

// NameValue returns the value of key as a decoded name.
func (d *Dict) NameValue(key string) string {
	n, _ := d.Get(key).(Name)
	return string(n)
}

// StringValue returns key's value as a literal or hexadecimal text string,
// or "" when the key is absent or holds another type.
func (d *Dict) StringValue(key string) string {
	if s, ok := d.Get(key).(String); ok {
		return string(s)
	}
	return ""
}

// IntValue returns the value of key as an integer.
func (d *Dict) IntValue(key string) (int64, bool) {
	switch v := d.Get(key).(type) {
	case Int:
		return int64(v), true
	case Real:
		return int64(v), true
	}
	return 0, false
}

// ArrayValue returns the value of key as an array.
func (d *Dict) ArrayValue(key string) Array {
	if a, ok := d.Get(key).(Array); ok {
		return a
	}
	return nil
}

// DictValue returns the value of key as a dictionary, dereferencing an
// indirect reference if f is supplied.
func (d *Dict) DictValue(key string, f *File) *Dict {
	v := d.Get(key)
	if r, ok := v.(Ref); ok && f != nil {
		v = f.Object(r.Num)
	}
	if dd, ok := v.(*Dict); ok {
		return dd
	}
	return nil
}

// streamValue returns the value of key as a stream, dereferencing if needed.
func (d *Dict) streamValue(key string, f *File) *Stream {
	v := d.Get(key)
	if r, ok := v.(Ref); ok && f != nil {
		v = f.Object(r.Num)
	}
	if s, ok := v.(*Stream); ok {
		return s
	}
	return nil
}

// formatName renders a PDF name, escaping what cannot appear literally.
func formatName(s string) string {
	var b []byte
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c <= 0x20 || c >= 0x7f || c == '#' || c == '(' || c == ')' || c == '<' || c == '>' ||
			c == '[' || c == ']' || c == '{' || c == '}' || c == '/' || c == '%' {
			b = append(b, '#')
			b = append(b, hexDigits[c>>4], hexDigits[c&0xf])
			continue
		}
		b = append(b, c)
	}
	return "/" + string(b)
}

const hexDigits = "0123456789abcdef"

// formatString renders a byte string, choosing a literal or hexadecimal form.
func formatString(s string) string {
	printable := true
	for i := 0; i < len(s); i++ {
		if s[i] < 0x20 || s[i] > 0x7e {
			printable = false
			break
		}
	}
	if printable {
		var b []byte
		for i := 0; i < len(s); i++ {
			switch c := s[i]; c {
			case '(', ')', '\\':
				b = append(b, '\\', c)
			default:
				b = append(b, c)
			}
		}
		return "(" + string(b) + ")"
	}
	b := make([]byte, 0, 2+2*len(s))
	b = append(b, '<')
	for i := 0; i < len(s); i++ {
		b = append(b, hexDigits[s[i]>>4], hexDigits[s[i]&0xf])
	}
	b = append(b, '>')
	return string(b)
}

// formatReal renders a real number without an exponent, which ISO 32000
// number syntax does not allow.
func formatReal(v float64) string {
	return strconv.FormatFloat(v, 'f', -1, 64)
}

// ErrUnsupported reports a construct this package deliberately does not
// handle. Callers can test for it with errors.Is.
var ErrUnsupported = errors.New("pdf: unsupported feature")

// StreamValue returns the value of key as a stream, dereferencing an indirect
// reference through f when it is not nil.
func (d *Dict) StreamValue(key string, f *File) *Stream { return d.streamValue(key, f) }
