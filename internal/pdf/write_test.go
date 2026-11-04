package pdf

import (
	"bytes"
	"errors"
	"strings"
	"testing"
)

// writeValue is the whole of the output syntax, so its table is worth
// pinning down case by case: every branch here is a construct a PDF/A
// validator will complain about if it is rendered wrongly.
func TestWriteValueSyntax(t *testing.T) {
	cases := []struct {
		name string
		in   Object
		want string
	}{
		{"missing value", nil, "null"},
		{"null object", Null{}, "null"},
		{"true", Bool(true), "true"},
		{"false", Bool(false), "false"},
		{"int", Int(-2147483648), "-2147483648"},
		{"real", Real(0.25), "0.25"},
		{"real whole", Real(3), "3"},
		{"name", Name("Type"), "/Type"},
		{"name escapes", Name("A B/C%D#E"), "/A#20B#2fC#25D#23E"},
		{"name braces and angle", Name("{a}<b>"), "/#7ba#7d#3cb#3e"},
		{"name del", Name("a\x7fb"), "/a#7fb"},
		{"literal string", String("Invoice 12"), "(Invoice 12)"},
		{"literal string escapes", String("a(b)c\\d"), "(a\\(b\\)c\\\\d)"},
		{"hex string for control bytes", String("a\nb"), "<610a62>"},
		{"hex string for high bytes", String("é"), "<c3a9>"},
		{"empty string", String(""), "()"},
		{"reference", Ref{Num: 12, Gen: 3}, "12 3 R"},
		{"array", Array{Int(1), Real(2.5), Name("x")}, "[1 2.5 /x]"},
		{"empty array", Array{}, "[]"},
		{"empty dict", NewDict(), "<< >>"},
		{"dict sorted", DictFrom(Name("Z"), Int(1), Name("A"), Int(2)), "<< /A 2 /Z 1 >>"},
		{"plain go int", int(12), "12"},
		{"plain go string", string("hi"), "(hi)"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var out bytes.Buffer
			if err := writeValue(&out, c.in); err != nil {
				t.Fatalf("writeValue(%v): %v", c.in, err)
			}
			if got := out.String(); got != c.want {
				t.Errorf("writeValue(%v) = %q, want %q", c.in, got, c.want)
			}
		})
	}
}

func TestWriteValueErrors(t *testing.T) {
	cases := []struct {
		name string
		in   Object
		want string
	}{
		{"unsupported type", float64(1.5), "cannot write float64"},
		{"stream without dictionary", &Stream{Raw: []byte("x")}, "stream without dictionary"},
		{"nested in array", Array{Int(1), float64(2)}, "cannot write float64"},
		{"nested in dict", DictFrom(Name("K"), float64(2)), "cannot write float64"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var out bytes.Buffer
			err := writeValue(&out, c.in)
			if err == nil {
				t.Fatalf("writeValue(%v) succeeded, want an error", c.in)
			}
			if !strings.Contains(err.Error(), c.want) {
				t.Errorf("error = %q, want it to mention %q", err, c.want)
			}
		})
	}
	if err := writeValue(&bytes.Buffer{}, float64(1)); !errors.Is(err, ErrUnsupported) {
		t.Errorf("unsupported value error = %v, want ErrUnsupported", err)
	}
}

func TestWriteStreamBody(t *testing.T) {
	var out bytes.Buffer
	s := NewStream([]byte("abc"), Name("Type"), Name("ObjStm"))
	if err := writeValue(&out, s); err != nil {
		t.Fatal(err)
	}
	want := "<< /Type /ObjStm >>\nstream\nabc\nendstream"
	if out.String() != want {
		t.Errorf("stream = %q, want %q", out.String(), want)
	}
}

func TestFmtOffset(t *testing.T) {
	cases := map[int]string{0: "0000000000", 7: "0000000007", 1234567890: "1234567890"}
	for in, want := range cases {
		if got := fmtOffset(in); got != want {
			t.Errorf("fmtOffset(%d) = %q, want %q", in, got, want)
		}
	}
	if got := fmtOffset(12345678901); len(got) != 11 {
		t.Errorf("fmtOffset must not truncate, got %q", got)
	}
}

func TestCompressAndDecompress(t *testing.T) {
	body := bytes.Repeat([]byte("Factur-X invoice data. "), 40)
	f := New("1.7")
	s := NewStream(append([]byte(nil), body...))
	f.Add(s)

	if err := s.Compress(-1); err != nil {
		t.Fatalf("Compress: %v", err)
	}
	if got := s.Dict.NameValue("Filter"); got != "FlateDecode" {
		t.Errorf("Filter = %q after Compress, want FlateDecode", got)
	}
	if bytes.Equal(s.Raw, body) {
		t.Error("Compress left the body uncompressed")
	}
	plain, err := f.Decompress(s)
	if err != nil {
		t.Fatalf("Decompress: %v", err)
	}
	if !bytes.Equal(plain.Raw, body) {
		t.Errorf("round trip changed the body: got %d bytes, want %d", len(plain.Raw), len(body))
	}
	if plain.Dict.Has("Filter") {
		t.Error("Decompress kept /Filter")
	}
	if !s.Dict.Has("Filter") {
		t.Error("Decompress must not modify the original stream")
	}
}

func TestCompressKeepsIncompressibleData(t *testing.T) {
	// Incompressible bytes cost more as a flate stream than they do raw,
	// because the container and block headers alone exceed the slack the
	// code allows. Such a stream must stay stored and any filter entry
	// must be dropped.
	raw := incompressible(70000)
	s := NewStream(raw, Name("Filter"), Name("ASCIIHexDecode"), Name("DecodeParms"), NewDict())
	if err := s.Compress(-1); err != nil { // -1 is flate.DefaultCompression
		t.Fatalf("Compress: %v", err)
	}
	if s.Dict.Has("Filter") || s.Dict.Has("DecodeParms") {
		t.Errorf("stored stream still names filters: %v", s.Dict.Keys())
	}
	if !bytes.Equal(s.Raw, raw) {
		t.Error("stored stream was replaced")
	}
}

// incompressible returns n bytes from a deterministic xorshift64 generator.
func incompressible(n int) []byte {
	out := make([]byte, n)
	state := uint64(0x2545f4914f6cdd1d)
	for i := range out {
		state ^= state << 13
		state ^= state >> 7
		state ^= state << 17
		out[i] = byte(state >> 24)
	}
	return out
}

func TestCompressBadLevel(t *testing.T) {
	s := NewStream([]byte("hello hello hello"))
	if err := s.Compress(99); err == nil {
		t.Fatal("Compress(99) succeeded, want an invalid level error")
	}
}

func TestDecompressUnknownFilter(t *testing.T) {
	f := New("1.7")
	s := NewStream([]byte("junk"), Name("Filter"), Name("Whirlpool"))
	if _, err := f.Decompress(s); err == nil {
		t.Fatal("Decompress of an unknown filter succeeded, want an error")
	}
}

func TestTextUTF16(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"", "\xfe\xff"},
		{"A", "\xfe\xff\x00A"},
		{"£", "\xfe\xff\x00\xa3"},
		{"😀", "\xfe\xff\xd8\x3d\xde\x00"},
	}
	for _, c := range cases {
		if got := string(TextUTF16(c.in)); got != c.want {
			t.Errorf("TextUTF16(%q) = %q, want %q", c.in, got, c.want)
		}
	}
	// A byte order mark is not printable, so it must go out hexadecimal.
	if got := formatString(string(TextUTF16("x"))); !strings.HasPrefix(got, "<feff") {
		t.Errorf("TextUTF16 rendered as %q, want hexadecimal", got)
	}
}

func TestBytesNeedsCatalog(t *testing.T) {
	f := New("")
	if f.Version != "1.7" {
		t.Errorf("New(\"\") version = %q, want 1.7", f.Version)
	}
	f.Trailer.Delete("Root")
	if _, err := f.Bytes(); err == nil {
		t.Fatal("Bytes without a catalog succeeded, want an error")
	}
}

func TestBytesReportsBadObjects(t *testing.T) {
	f := New("1.7")
	f.Add(&Stream{Raw: []byte("body")})
	if _, err := f.Bytes(); err == nil || !strings.Contains(err.Error(), "has no dictionary") {
		t.Fatalf("Bytes with a dictionary-less stream = %v, want a dictionary error", err)
	}

	g := New("1.7")
	g.Add(float64(1.5))
	if _, err := g.Bytes(); err == nil || !strings.Contains(err.Error(), "cannot write float64") {
		t.Fatalf("Bytes with an unwritable object = %v, want a write error", err)
	}

	h := New("1.7")
	h.Trailer.Set("Odd", float64(1.5))
	if _, err := h.Bytes(); err == nil || !strings.Contains(err.Error(), "trailer") {
		t.Fatalf("Bytes with an unwritable trailer entry = %v, want a trailer error", err)
	}
}

func TestBytesBuildsTrailerIDAndGaps(t *testing.T) {
	f := New("2.0")
	page := NewDict()
	page.Set("Type", Name("Page"))
	num := f.Add(page)
	f.Delete(num) // leaves a hole in the object table
	page2 := NewDict()
	page2.Set("Type", Name("Page"))
	f.Add(page2)

	data, err := f.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.HasPrefix(data, []byte("%PDF-2.0\n%\xe2\xe3\xcf\xd3\n")) {
		t.Errorf("header = %q", data[:20])
	}
	if !bytes.Contains(data, []byte("/ID [<")) {
		t.Error("trailer has no /ID")
	}
	if got := bytes.Count(data, []byte("0000000000 65535 f \n")); got != 2 {
		t.Errorf("free entries = %d, want 2 (table head plus the deleted object)", got)
	}

	again, err := Parse(data)
	if err != nil {
		t.Fatalf("reparse: %v", err)
	}
	if again.Version != "2.0" {
		t.Errorf("version after round trip = %q", again.Version)
	}
	if r, err := again.Root(); err != nil || r.TypeName() != "Catalog" {
		t.Errorf("catalog after round trip: %v, %v", r, err)
	}
	if id := again.Trailer.ArrayValue("ID"); len(id) != 2 {
		t.Errorf("/ID = %v, want two elements", id)
	}
	// A second serialisation must be byte-identical.
	second, err := again.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(second, []byte("/ID [<")) {
		t.Error("round-tripped trailer lost its /ID")
	}
}

func TestDictAccessors(t *testing.T) {
	f := New("1.7")
	inner := NewDict()
	inner.Set("Type", Name("Pages"))
	num := f.Add(inner)
	stream := NewStream([]byte("s"), Name("Type"), Name("ObjStm"))
	snum := f.Add(stream)

	d := DictFrom(
		Name("Title"), String("Invoice"),
		Name("Kids"), Array{Int(1)},
		Name("Ref"), Ref{Num: num},
		Name("Stream"), Ref{Num: snum},
		Name("Count"), Real(7),
		Name("Nope"), Bool(true),
	)
	if d.Len() != 6 {
		t.Errorf("Len = %d, want 6", d.Len())
	}
	if got := d.StringValue("Title"); got != "Invoice" {
		t.Errorf("StringValue = %q", got)
	}
	if got := d.StringValue("Nope"); got != "" {
		t.Errorf("StringValue of a bool = %q, want empty", got)
	}
	if got := d.ArrayValue("Kids"); len(got) != 1 {
		t.Errorf("ArrayValue = %v", got)
	}
	if got := d.ArrayValue("Title"); got != nil {
		t.Errorf("ArrayValue of a string = %v, want nil", got)
	}
	if n, ok := d.IntValue("Count"); !ok || n != 7 {
		t.Errorf("IntValue(Count) = %d, %v", n, ok)
	}
	if n, ok := d.IntValue("Nope"); ok || n != 0 {
		t.Errorf("IntValue of a bool = %d, %v", n, ok)
	}
	if got := d.DictValue("Ref", f); got == nil || got.TypeName() != "Pages" {
		t.Errorf("DictValue through a reference = %v", got)
	}
	if got := d.DictValue("Stream", nil); got != nil {
		t.Errorf("DictValue of an undereferenced stream = %v, want nil", got)
	}
	if got := d.StreamValue("Stream", f); got == nil || string(got.Raw) != "s" {
		t.Errorf("StreamValue through a reference = %v", got)
	}
	if got := d.TypeName(); got != "" {
		t.Errorf("TypeName of a dict with no /Type = %q", got)
	}

	var empty *Dict
	if empty.Has("x") || empty.Get("x") != nil || empty.Keys() != nil || empty.SortedKeys() != nil || empty.Len() != 0 {
		t.Error("nil dictionary should behave as empty")
	}
	if empty.TypeName() != "" || empty.streamValue("x", f) != nil {
		t.Error("nil dictionary accessors should return zero values")
	}
	if empty.DictValue("x", f) != nil {
		t.Error("DictValue on a nil dictionary should return nil")
	}
}

func TestFileObjectTable(t *testing.T) {
	f := New("1.7")
	a := f.Add(Int(1))
	b := f.Add(Int(2))
	if a >= b {
		t.Fatalf("Add returned %d then %d", a, b)
	}
	if f.Object(b) != Int(2) {
		t.Errorf("Object(%d) = %v", b, f.Object(b))
	}
	f.Set(a+10, Int(9))
	if f.Object(a+10) != Int(9) {
		t.Error("Set did not store a new object number")
	}
	f.Delete(a)
	if f.Object(a) != nil {
		t.Error("Delete left the object in place")
	}
	want := []int{1, a + 1, a + 10} // the catalog stays at 1
	if got := f.nums; !equalInts(got, want) {
		t.Errorf("object numbers = %v, want %v", got, want)
	}
	if f.Object(9999) != nil {
		t.Error("Object of an unused number should be nil")
	}
	ref := Ref{Num: b}
	if got := f.Resolve(ref); got != Int(2) {
		t.Errorf("Resolve = %v", got)
	}
	missing := Ref{Num: 9999}
	if resolved := f.Resolve(missing); resolved != (Null{}) {
		t.Errorf("Resolve of a missing object = %v, want null", resolved)
	}
	direct := Int(5)
	if got := f.Resolve(direct); got != direct {
		t.Errorf("Resolve of a direct value = %v", got)
	}
	s := Ref{Num: 4, Gen: 1}
	if got := s.String(); got != "4 1 R" {
		t.Errorf("Ref.String = %q", s)
	}
}

func equalInts(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
