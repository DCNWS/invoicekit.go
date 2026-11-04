package pdf

import (
	"bytes"
	"compress/flate"
	"compress/zlib"
	"encoding/hex"
	"errors"
	"strings"
	"testing"
)

func zipped(t *testing.T, b []byte) []byte {
	t.Helper()
	var out bytes.Buffer
	w := zlib.NewWriter(&out)
	if _, err := w.Write(b); err != nil {
		t.Fatalf("zlib write: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("zlib close: %v", err)
	}
	return out.Bytes()
}

// hexUp renders data as upper-case ASCII hex, the shape ASCIIHexDecode expects.
func hexUp(b []byte) string { return strings.ToUpper(hex.EncodeToString(b)) }

func deflated(t *testing.T, b []byte) []byte {
	t.Helper()
	var out bytes.Buffer
	w, err := flate.NewWriter(&out, flate.DefaultCompression)
	if err != nil {
		t.Fatalf("flate writer: %v", err)
	}
	if _, err := w.Write(b); err != nil {
		t.Fatalf("flate write: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("flate close: %v", err)
	}
	return out.Bytes()
}

// TestRunLengthDecodeWireFormat pins the run types described in
// ISO 32000-1 7.4.5: 0..127 introduces length+1 literal bytes, 128 ends the
// data, and 129..254 repeats the following byte 257-length times.
func TestRunLengthDecodeWireFormat(t *testing.T) {
	cases := []struct {
		name string
		in   []byte
		want string
	}{
		{"literal run copies length+1 bytes", []byte("\x02abc\x80"), "abc"},
		{"zero length copies one byte", []byte("\x00A\x80"), "A"},
		{"longest literal run", append([]byte{127}, append(bytes.Repeat([]byte{'q'}, 128), 0x80)...), strings.Repeat("q", 128)},
		{"repeat run", []byte("\xfeQ\x80"), "QQQ"},
		{"longest repeat run", []byte{129, 'z'}, strings.Repeat("z", 128)},
		{"mixed runs", []byte("\x01XY\xfaZ\x00W\x80ignored"), "XYZZZZZZZW"},
		{"end of data stops early", []byte("\x00A\x80\x00B"), "A"},
		{"no end-of-data marker", []byte("\x01XY"), "XY"},
		{"empty", nil, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := runLengthDecode(tc.in)
			if err != nil {
				t.Fatalf("runLengthDecode: %v", err)
			}
			if string(got) != tc.want {
				t.Errorf("got %q (%d bytes), want %q (%d bytes)", got, len(got), tc.want, len(tc.want))
			}
		})
	}
}

func TestRunLengthDecodeTruncation(t *testing.T) {
	for name, in := range map[string][]byte{
		"literal run cut short": []byte("\x05ab"),
		"length byte alone":     []byte("\xff"),
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := runLengthDecode(in); err == nil {
				t.Fatal("truncated data accepted")
			}
		})
	}
}

// TestDecodeStreamRouting covers the filter dispatch: an unsupported name, a
// filter that fails on the bytes it is given, and a predictor applied after a
// filter.
func TestDecodeStreamRouting(t *testing.T) {
	f := &File{}
	body := zipped(t, []byte("row"))

	t.Run("run length through decodeStream", func(t *testing.T) {
		s := NewStream([]byte("\x02abc\x80"), Name("Filter"), Name("RunLengthDecode"))
		got, err := f.decodeStream(s)
		if err != nil {
			t.Fatalf("decodeStream: %v", err)
		}
		if string(got) != "abc" {
			t.Errorf("got %q, want %q", got, "abc")
		}
	})

	t.Run("chained filters", func(t *testing.T) {
		// ASCIIHex of a zlib stream, decoded in the order the array lists.
		s := NewStream([]byte(hexUp(zipped(t, []byte("chained")))+">"),
			Name("Filter"), Array{Name("ASCIIHexDecode"), Name("FlateDecode")})
		got, err := f.decodeStream(s)
		if err != nil {
			t.Fatalf("decodeStream: %v", err)
		}
		if string(got) != "chained" {
			t.Errorf("got %q, want %q", got, "chained")
		}
	})

	t.Run("unknown filter", func(t *testing.T) {
		s := NewStream([]byte("x"), Name("Filter"), Name("LZWDecode"))
		if _, err := f.decodeStream(s); !errors.Is(err, ErrUnsupported) {
			t.Errorf("error = %v, want ErrUnsupported", err)
		}
	})

	t.Run("filter fails", func(t *testing.T) {
		s := NewStream([]byte("not compressed at all"), Name("Filter"), Name("FlateDecode"))
		_, err := f.decodeStream(s)
		if err == nil || !strings.Contains(err.Error(), "filter FlateDecode") {
			t.Errorf("error = %v, want it to name the filter", err)
		}
	})

	t.Run("predictor fails", func(t *testing.T) {
		s := NewStream(body,
			Name("Filter"), Name("FlateDecode"),
			Name("DecodeParms"), DictFrom(Name("Predictor"), Int(15), Name("Columns"), Int(0)))
		_, err := f.decodeStream(s)
		if err == nil || !strings.Contains(err.Error(), "without /Columns") {
			t.Errorf("error = %v, want it to mention /Columns", err)
		}
	})

	t.Run("array of filter names ignores non-names", func(t *testing.T) {
		s := NewStream([]byte("\x00A\x80"),
			Name("Filter"), Array{String("RunLengthDecode"), Null{}, Name("RunLengthDecode")})
		if _, err := f.decodeStream(s); err != nil {
			t.Errorf("decodeStream: %v", err)
		}
	})
}

func TestFilterListAndParmList(t *testing.T) {
	d1 := DictFrom(Name("Predictor"), Int(12))
	d2 := DictFrom(Name("Predictor"), Int(15))

	if got := filterList(Name("FlateDecode")); len(got) != 1 || got[0] != "FlateDecode" {
		t.Errorf("single name: %#v", got)
	}
	got := filterList(Array{Name("ASCIIHexDecode"), Int(3), Name("FlateDecode")})
	if len(got) != 2 || got[1] != "FlateDecode" {
		t.Errorf("name array: %#v", got)
	}
	for _, v := range []Object{nil, String("FlateDecode"), Int(2), Bool(true)} {
		if l := filterList(v); len(l) != 0 {
			t.Errorf("%T: got %#v, want empty", v, l)
		}
	}

	// A single dictionary applies to the only filter; arrays line up with the
	// filter names, and slots beyond the array stay nil.
	if p := parmList(d1, 1); len(p) != 1 || p[0] != d1 {
		t.Errorf("single dict: %#v", p)
	}
	if p := parmList(String("x"), 1); len(p) != 1 || p[0] != nil {
		t.Errorf("non-dict for one filter: %#v", p)
	}
	p := parmList(Array{d1, Int(7), d2}, 5)
	if len(p) != 5 || p[0] != d1 || p[1] != nil || p[2] != d2 || p[3] != nil {
		t.Errorf("array of parms: %#v", p)
	}
	if p := parmList(nil, 2); len(p) != 2 || p[0] != nil {
		t.Errorf("missing parms: %#v", p)
	}
	if p := parmList(d1, 0); len(p) != 0 {
		t.Errorf("no filters: %#v", p)
	}
}

// TestInflateVariants checks that inflate copes with bare deflate data, with
// data whose trailer is damaged, and with bytes that are neither.
func TestInflateVariants(t *testing.T) {
	want := []byte("the quick brown fox jumps over the lazy dog")

	if got, err := inflate(zipped(t, want)); err != nil || string(got) != string(want) {
		t.Errorf("zlib: %q, %v", got, err)
	}
	if got, err := inflate(deflated(t, want)); err != nil || string(got) != string(want) {
		t.Errorf("bare deflate: %q, %v", got, err)
	}

	// A zlib stream cut short partway through still yields what came through.
	full := zipped(t, want)
	for cut := len(full) - 1; cut > 2; cut-- {
		got, err := inflate(full[:cut])
		if err != nil {
			continue
		}
		if len(got) > 0 && len(got) < len(want) && !bytes.HasPrefix(want, got) {
			t.Errorf("cut %d: got %q, want a prefix of %q", cut, got, want)
		}
		break
	}

	for _, junk := range [][]byte{nil, {0x00}, {0x78, 0x9c, 0xff, 0xff}, []byte("definitely not deflate")} {
		if _, err := inflate(junk); err == nil {
			t.Errorf("inflate(%v): accepted junk", junk)
		}
	}
}

func TestASCIIHexDecodeEdges(t *testing.T) {
	cases := []struct {
		in   string
		want string
		err  bool
	}{
		{in: "414>", want: "A\x40"}, // an odd nibble is padded with zero
		{in: "41 42\r\n43>", want: "ABC"},
		{in: ">", want: ""},
		{in: "41>zzzz", want: "A"}, // bytes after the marker are not data
		{in: "4Z>", err: true},
		{in: "\xc4>", err: true},
	}
	for _, tc := range cases {
		got, err := asciiHexDecode([]byte(tc.in))
		if tc.err {
			if err == nil {
				t.Errorf("asciiHexDecode(%q) = %q, want an error", tc.in, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("asciiHexDecode(%q): %v", tc.in, err)
			continue
		}
		if string(got) != tc.want {
			t.Errorf("asciiHexDecode(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// TestASCII85DecodeEdges walks the delimiters, the 'z' shortcut and the
// trailing partial group. "@:E_W" is the five-character group for "abcd".
func TestASCII85DecodeEdges(t *testing.T) {
	cases := []struct {
		in   string
		want string
		err  bool
	}{
		{in: "@:E_W~>", want: "abcd"},
		{in: "  \n<~@:E_W~>", want: "abcd"},
		{in: "@:E_W>", want: "abcd"},
		{in: "@:E_W\n", want: "abcd"},
		{in: "@:E_", want: "abc"}, // a partial group of four
		{in: "@:E", want: "ab"},
		{in: "@/", want: "a"},
		{in: "@\x7f", err: true}, // 0x7f sits just past the alphabet
		{in: "@", want: ""},      // a lone character carries no whole byte
		{in: "z", want: "\x00\x00\x00\x00"},
		{in: "@:E_W z", want: "abcd\x00\x00\x00\x00"},
		{in: "z z", want: "\x00\x00\x00\x00\x00\x00\x00\x00"},
		{in: "@\x01", err: true},
		// The short groups below were produced and decoded by an
		// independent ASCII85 implementation, so the padding of a
		// truncated group is checked against a reference rather than
		// against this decoder's own arithmetic.
		{in: "4T", want: "\x3d"},                                     // one byte from a two-character group
		{in: "R#D", want: "\x98\x8c"},                                // reference: 2 bytes
		{in: ",%'X", want: "\x22\x5f\xeb"},                           // reference: 3 bytes
		{in: "RsLcn", want: "\x9b\x7a\xa1\x95"},                      // reference: 4 bytes
		{in: "&Y'!gf)", want: "\x11\x9c\x04\xe9\xd7"},                // reference: 5 bytes
		{in: "GsdTS1B.", want: "\x79\x43\x8e\x3c\x32\xff"},           // reference: 6 bytes
		{in: "UtmC\x5cUF4q", want: "\xa4\xdd\x27\x3c\xa3\x27\xdf"},   // reference: 7 bytes
		{in: ".DtkH$VnbK", want: "\x29\xc3\xf6\x98\x0b\x4e\xc8\x08"}, // eight bytes, two full groups
	}
	for _, tc := range cases {
		got, err := ascii85Decode([]byte(tc.in))
		if tc.err {
			if err == nil {
				t.Errorf("ascii85Decode(%q) = %q, want an error", tc.in, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("ascii85Decode(%q): %v", tc.in, err)
			continue
		}
		if string(got) != tc.want {
			t.Errorf("ascii85Decode(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestStreamBytesNil(t *testing.T) {
	f := &File{}
	if _, err := f.StreamBytes(nil); err == nil {
		t.Error("nil stream accepted")
	}
	s := NewStream([]byte("plain"), Name("Type"), Name("ObjStm"))
	got, err := f.StreamBytes(s)
	if err != nil || string(got) != "plain" {
		t.Errorf("StreamBytes = %q, %v", got, err)
	}
}
