package pdf

import (
	"bytes"
	"strings"
	"testing"
)

// fragment parses the single object at the start of text and hands back the
// scanner positioned just after its value, which is where readStream expects
// to be called from. These paths are hard to reach through Parse, because a
// whole document has to be malformed in a very specific way; driving the
// functions directly keeps the error branches honest.
func fragment(t *testing.T, text string) (*File, *scanner, Object, int) {
	t.Helper()
	f, r, num := fragmentAtHeader(t, text)
	v, err := r.value(f)
	if err != nil {
		t.Fatalf("value: %v", err)
	}
	r.skipToToken()
	return f, r, v, num
}

// fragmentAtHeader stops after the "num gen obj" header, with the value still
// unread.
func fragmentAtHeader(t *testing.T, text string) (*File, *scanner, int) {
	t.Helper()
	data := []byte(text)
	f := &File{objs: map[int]Object{}, table: map[int]xrefEntry{}, chars: data}
	r := &scanner{data: data}
	num, _, err := r.objectHeader()
	if err != nil {
		t.Fatalf("object header: %v", err)
	}
	return f, r, num
}

func TestReadStreamNeedsStreamKeyword(t *testing.T) {
	f, r, v, num := fragment(t, "1 0 obj\n<< /Length 3 >>\nnope\nabc\nendstream\nendobj")
	d, ok := v.(*Dict)
	if !ok {
		t.Fatalf("value is %T, want a dictionary", v)
	}
	if _, err := f.readStream(f.chars, r, d, num, 0); err == nil || !strings.Contains(err.Error(), "expected stream keyword") {
		t.Fatalf("readStream = %v, want a missing-keyword error", err)
	}
}

func TestReadStreamNeedsEndstream(t *testing.T) {
	f, r, v, num := fragment(t, "2 0 obj\n<< >>\nstream\nabc\nendobj")
	d, _ := v.(*Dict)
	if _, err := f.readStream(f.chars, r, d, num, 0); err == nil || !strings.Contains(err.Error(), "no usable /Length") {
		t.Fatalf("readStream = %v, want a missing-length error", err)
	}
}

func TestReadStreamDictionarySuppliedByScanner(t *testing.T) {
	text := "3 0 obj\n<< /Length 2 >>\nstream\nhi\nendstream\nendobj"
	f, r, num := fragmentAtHeader(t, text)
	// The dictionary is not known yet: readStream must read it itself.
	s, err := f.readStream(f.chars, r, nil, num, 0)
	if err != nil {
		t.Fatalf("readStream: %v", err)
	}
	if string(s.Raw) != "hi" {
		t.Errorf("stream body = %q, want %q", s.Raw, "hi")
	}
	if s.Dict.NameValue("Length") != "" {
		t.Error("stream dictionary lost its entries")
	}
}

func TestReadStreamWithoutDictionary(t *testing.T) {
	f, r, num := fragmentAtHeader(t, "4 0 obj\n[1 2]\nstream\nhi\nendstream\nendobj")
	if _, err := f.readStream(f.chars, r, nil, num, 0); err == nil || !strings.Contains(err.Error(), "stream without dictionary") {
		t.Fatalf("readStream = %v, want a dictionary error", err)
	}
}

func TestObjectForLoadsFromTable(t *testing.T) {
	body := "<< /Kind /Spare >>"
	doc := buildClassic2("1.4", []string{
		"<< /Type /Catalog /Pages 2 0 R >>",
		"<< /Type /Pages /Kids [] /Count 0 >>",
		"<< /Length 0 >>\nstream\n\nendstream",
		"<< /Unused 1 >>",
		body,
	}, "")
	at := bytes.Index(doc, []byte("5 0 obj"))
	if at < 0 {
		t.Fatal("spare object missing from the fixture")
	}
	f := &File{objs: map[int]Object{}, chars: doc, table: map[int]xrefEntry{5: {offset: at, seen: true}}}

	obj, err := f.objectFor(5)
	if err != nil {
		t.Fatalf("objectFor: %v", err)
	}
	d, ok := obj.(*Dict)
	if !ok || d.NameValue("Kind") != "Spare" {
		t.Fatalf("loaded object = %v, want the spare dictionary", obj)
	}
	if f.Object(5) == nil {
		t.Error("objectFor should cache what it loaded")
	}
	again, err := f.objectFor(5)
	if err != nil || again != obj {
		t.Errorf("second objectFor = %v, %v, want the cached object", again, err)
	}
}

func TestObjectForErrors(t *testing.T) {
	doc := simpleClassic(t)
	f := &File{objs: map[int]Object{}, chars: doc, table: map[int]xrefEntry{}}
	if _, err := f.objectFor(9); err == nil || !strings.Contains(err.Error(), "object 9 not found") {
		t.Errorf("objectFor of an unmapped number = %v", err)
	}
	f.table[10] = xrefEntry{offset: 0}
	if _, err := f.objectFor(10); err == nil || !strings.Contains(err.Error(), "object 10 not found") {
		t.Errorf("objectFor of a free entry = %v", err)
	}
	f.table[11] = xrefEntry{offset: len(doc) - 6}
	if _, err := f.objectFor(11); err == nil {
		t.Error("objectFor of a corrupt offset should report a parse error")
	}
}

func TestSetEntryKeepsNewestSection(t *testing.T) {
	f := &File{objs: map[int]Object{}, table: map[int]xrefEntry{}}
	f.setEntry(4, xrefEntry{offset: 100, seen: true})
	f.setEntry(4, xrefEntry{offset: 200})
	if got := f.table[4].offset; got != 100 {
		t.Errorf("offset = %d, want the entry from the newer section (100)", got)
	}
	f.setEntry(5, xrefEntry{offset: 100})
	f.setEntry(5, xrefEntry{offset: 200, seen: true})
	if got := f.table[5].offset; got != 200 {
		t.Errorf("offset = %d, want 200: unseen entries are replaced", got)
	}
}

func TestOrInt(t *testing.T) {
	if got := orInt(Int(-3)); got != -3 {
		t.Errorf("orInt(Int) = %d", got)
	}
	if got := orInt(Real(2.9)); got != 2 {
		t.Errorf("orInt(Real) = %d, want the truncated integer", got)
	}
	for _, v := range []Object{nil, Name("x"), String("1"), Bool(true)} {
		if got := orInt(v); got != 0 {
			t.Errorf("orInt(%v) = %d, want 0", v, got)
		}
	}
}

func TestParseNumberToken(t *testing.T) {
	if _, err := parseNumberToken("", 5); err == nil || !strings.Contains(err.Error(), "expected a number at offset 5") {
		t.Errorf("parseNumberToken(\"\") = %v", err)
	}
	if v, err := parseNumberToken("12", 0); err != nil || v != Int(12) {
		t.Errorf("parseNumberToken(12) = %v, %v", v, err)
	}
	if v, err := parseNumberToken("-1.5", 0); err != nil || v != Real(-1.5) {
		t.Errorf("parseNumberToken(-1.5) = %v, %v", v, err)
	}
	if v, err := parseNumberToken("9223372036854775808", 0); err != nil {
		t.Errorf("an out-of-range integer should fall back to a real number: %v", err)
	} else if _, ok := v.(Real); !ok {
		t.Errorf("parseNumberToken of a huge number = %T, want Real", v)
	}
	if _, err := parseNumberToken("1.2.3", 0); err == nil || !strings.Contains(err.Error(), "bad real number") {
		t.Errorf("parseNumberToken(1.2.3) = %v", err)
	}
	if _, err := parseNumberToken("abc", 0); err == nil || !strings.Contains(err.Error(), "bad number") {
		t.Errorf("parseNumberToken(abc) = %v", err)
	}
}

func TestScannerSkipsWhitespaceAndComments(t *testing.T) {
	r := &scanner{data: []byte("  % a comment\n\tfoo bar")}
	r.skipToToken()
	if r.pos != len("  % a comment\n\t") {
		t.Errorf("pos = %d, want it parked on the token", r.pos)
	}
	if !r.keyword("foo") {
		t.Error("keyword(x) after a comment should be true")
	}
	eof := &scanner{data: []byte("   % comment with no newline")}
	eof.skipToToken()
	if eof.pos != len(eof.data) {
		t.Errorf("pos = %d, want the end of the data", eof.pos)
	}
	if eof.keyword("x") {
		t.Error("keyword at end of data should be false")
	}
	short := &scanner{data: []byte("ab"), pos: 0}
	if !short.keyword("ab") {
		t.Error("a keyword running to the end of the data should match")
	}
	if short.keyword("abc") {
		t.Error("a keyword longer than the data should not match")
	}
}

func TestScannerNumberErrors(t *testing.T) {
	r := &scanner{data: []byte("/Name")}
	if _, err := r.intNumber(); err == nil || !strings.Contains(err.Error(), "expected a number") {
		t.Errorf("intNumber at a name = %v", err)
	}
	r = &scanner{data: []byte("12x rest")}
	if _, err := r.intNumber(); err == nil || !strings.Contains(err.Error(), "expected an integer") {
		t.Errorf("intNumber at a malformed token = %v", err)
	}
	r = &scanner{data: []byte("12 3")}
	if v, err := r.number(); err != nil || v != Int(12) {
		t.Errorf("number = %v, %v", v, err)
	}
	if _, err := (&scanner{data: []byte("()")}).number(); err == nil {
		t.Error("number at a delimiter should fail")
	}
}

func TestObjectHeaderErrors(t *testing.T) {
	if _, _, err := fragmentHeader("not numbers"); err == nil || !strings.Contains(err.Error(), "expected an integer") {
		t.Errorf("objectHeader of garbage = %v", err)
	}
	if _, _, err := fragmentHeader("1 2 nope"); err == nil || !strings.Contains(err.Error(), "expected obj keyword") {
		t.Errorf("objectHeader without obj = %v", err)
	}
	if _, _, err := fragmentHeader("1"); err == nil {
		t.Error("objectHeader with a missing generation should fail")
	}
}

func fragmentHeader(text string) (int, int, error) {
	r := &scanner{data: []byte(text)}
	return r.objectHeader()
}

func TestPNGPredictor15AcceptsTheNoneFilter(t *testing.T) {
	// Rows tagged with filter 0 must come back untouched, which is the only
	// way a mixed stream can carry already-flat rows.
	data := encodeRows(img, []byte{0, 0})
	got, err := applyPredictor(data, predDict(Int(15)))
	if err != nil {
		t.Fatalf("applyPredictor: %v", err)
	}
	want := append(append([]byte(nil), img[0]...), img[1]...)
	if !bytes.Equal(got, want) {
		t.Errorf("rows = %v, want %v", got, want)
	}
}
