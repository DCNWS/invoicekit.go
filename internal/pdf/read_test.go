package pdf

import (
	"bytes"
	"compress/zlib"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"testing"
)

// buildClassic assembles a PDF with a classic cross-reference table from a
// list of object bodies, so tests do not have to hand-count offsets.
func buildClassic(version string, objs []string, trailerExtra string, extraTop string) []byte {
	var out bytes.Buffer
	out.WriteString("%PDF-" + version + "\n")
	offs := make([]int, len(objs))
	for i, body := range objs {
		offs[i] = out.Len()
		fmt.Fprintf(&out, "%d 0 obj\n%s\nendobj\n", i+1, body)
	}
	xrefPos := out.Len()
	out.WriteString("xref\n")
	fmt.Fprintf(&out, "0 %d\n", len(objs)+1)
	out.WriteString("0000000000 65535 f \n")
	for _, off := range offs {
		fmt.Fprintf(&out, "%010d 00000 n \n", off)
	}
	out.WriteString("trailer\n<< /Size " + fmt.Sprint(len(objs)+1) + " /Root 1 0 R" + trailerExtra + " >>\n")
	if extraTop != "" {
		out.WriteString(extraTop + "\n")
	}
	out.WriteString("startxref\n" + fmt.Sprint(xrefPos) + "\n%%EOF\n")
	return out.Bytes()
}

const pageBody = "<< /Type /Page /Parent 2 0 R /MediaBox [0 0 595 842] /Contents 4 0 R >>"

func simpleClassic(t *testing.T) []byte {
	t.Helper()
	content := "BT /F1 12 Tf 72 720 Td (Hello) Tj ET"
	objs := []string{
		"<< /Type /Catalog /Pages 2 0 R >>",
		"<< /Type /Pages /Kids [3 0 R] /Count 1 >>",
		pageBody,
		"<< /Length " + fmt.Sprint(len(content)) + " >>\nstream\n" + content + "\nendstream",
	}
	return buildClassic2("1.4", objs, "")
}

// buildClassic helper without the extraTop argument.
func buildClassic2(version string, objs []string, trailerExtra string) []byte {
	return buildClassic(version, objs, trailerExtra, "")
}

func TestParseClassic(t *testing.T) {
	f, err := Parse(simpleClassic(t))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if f.Version != "1.4" {
		t.Errorf("version = %q, want 1.4", f.Version)
	}
	root, err := f.Root()
	if err != nil {
		t.Fatal(err)
	}
	pages := root.DictValue("Pages", f)
	if pages == nil {
		t.Fatal("no Pages")
	}
	if n, _ := pages.IntValue("Count"); n != 1 {
		t.Errorf("Count = %d, want 1", n)
	}
	kids := pages.ArrayValue("Kids")
	if len(kids) != 1 {
		t.Fatalf("Kids = %v", kids)
	}
	page := f.Resolve(kids[0]).(*Dict)
	if page.NameValue("Type") != "Page" {
		t.Errorf("page type = %q", page.NameValue("Type"))
	}
	box := f.Resolve(page.Get("MediaBox")).(Array)
	if len(box) != 4 {
		t.Errorf("MediaBox = %v", box)
	}
	s := page.streamValue("Contents", f)
	if s == nil {
		t.Fatal("no contents stream")
	}
	body, err := f.decodeStream(s)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), "Hello") {
		t.Errorf("contents = %q", body)
	}
}

func TestRoundTripStable(t *testing.T) {
	f, err := Parse(simpleClassic(t))
	if err != nil {
		t.Fatal(err)
	}
	out1, err := f.Bytes()
	if err != nil {
		t.Fatalf("Bytes: %v", err)
	}
	f2, err := Parse(out1)
	if err != nil {
		t.Fatalf("reparse: %v\n%s", err, out1)
	}
	out2, err := f2.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(out1, out2) {
		t.Errorf("second write differs from first:\n%s\n----\n%s", out1, out2)
	}
	if !bytes.Contains(out1, []byte("%PDF-1.4")) {
		t.Errorf("header lost: %q", out1[:16])
	}
}

func TestAddObjectKeepsExisting(t *testing.T) {
	src := simpleClassic(t)
	f, err := Parse(src)
	if err != nil {
		t.Fatal(err)
	}
	root, _ := f.Root()
	num := f.Add(NewStream([]byte("factur-x"), Name("Type"), NewDict()))
	_ = num
	spec := f.Add(DictFrom(
		Name("Type"), Name("Filespec"),
		Name("F"), String("factur-x.xml"),
		Name("EF"), DictFrom(Name("F"), Ref{Num: num, Gen: 0}),
	))
	root.Set("PageMode", Name("UseAttachments"))
	root.Set("AF", Array{Ref{Num: spec, Gen: 0}})
	out, err := f.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	f2, err := Parse(out)
	if err != nil {
		t.Fatalf("reparse: %v\n%s", err, out)
	}
	root2, _ := f2.Root()
	if root2.NameValue("PageMode") != "UseAttachments" {
		t.Errorf("PageMode lost")
	}
	af := root2.ArrayValue("AF")
	if len(af) != 1 {
		t.Fatalf("AF = %v", af)
	}
	d := f2.Resolve(af[0]).(*Dict)
	if d.StringValue("F") != "factur-x.xml" {
		t.Errorf("filespec name = %q", d.StringValue("F"))
	}
	// The original objects must survive untouched.
	if f2.Object(3) == nil {
		t.Error("page object lost")
	}
	if !bytes.Contains(out, []byte("Hello")) {
		t.Error("content stream lost")
	}
}

func TestParseXrefStreamAndObjStm(t *testing.T) {
	data := buildXrefStream(t)
	f, err := Parse(data)
	if err != nil {
		t.Fatalf("Parse: %v\n%s", err, hexDumps(data))
	}
	root, err := f.Root()
	if err != nil {
		t.Fatal(err)
	}
	if root.TypeName() != "Catalog" {
		t.Errorf("root type = %q", root.TypeName())
	}
	pages := root.DictValue("Pages", f)
	if pages == nil {
		t.Fatal("Pages not reachable")
	}
	if n, _ := pages.IntValue("Count"); n != 1 {
		t.Errorf("Count = %d", n)
	}
	page := f.Resolve(pages.ArrayValue("Kids")[0]).(*Dict)
	s := page.streamValue("Contents", f)
	if s == nil {
		t.Fatal("no contents")
	}
	body, err := f.decodeStream(s)
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != "1 0 12 m f" {
		t.Errorf("contents = %q", body)
	}
	if f.Trailer.NameValue("Type") != "XRef" {
		t.Errorf("trailer type = %q", f.Trailer.TypeName())
	}
	out, err := f.Bytes()
	if err != nil {
		t.Fatalf("write: %v", err)
	}
	// The rewritten file must be readable by the same parser, which now sees a
	// classic cross-reference table instead of an XRef stream.
	f2, err := Parse(out)
	if err != nil {
		t.Fatalf("reparse: %v\n%s", err, out)
	}
	if _, err := f2.Root(); err != nil {
		t.Errorf("reparse root: %v", err)
	}
}

func TestEncryptedRefused(t *testing.T) {
	objs := []string{
		"<< /Type /Catalog /Pages 2 0 R >>",
		"<< /Type /Pages /Kids [3 0 R] /Count 1 >>",
		pageBody,
		"<< /Length 0 >>\nstream\n\nendstream",
		"<< /Filter /Standard /V 4 /R 4 /Length 128 >>",
	}
	data := buildClassic2("1.6", objs, " /Encrypt 5 0 R")
	_, err := Parse(data)
	if !errors.Is(err, ErrUnsupported) {
		t.Fatalf("error = %v, want ErrUnsupported", err)
	}
	if !strings.Contains(err.Error(), "encrypted") {
		t.Errorf("message = %q", err)
	}
}

func TestBrokenInputs(t *testing.T) {
	cases := map[string][]byte{
		"empty":            {},
		"not a pdf":        []byte("GIF89a..."),
		"no startxref":     []byte("%PDF-1.4\n1 0 obj << >> endobj\n%%EOF\n"),
		"bad startxref":    []byte("%PDF-1.4\nstartxref\n99999999\n%%EOF\n"),
		"truncated xref":   []byte("%PDF-1.4\nstartxref\n9\nxref\n0 5\n0000000000"),
		"bad entry":        []byte("%PDF-1.4\nstartxref\n9\nxref\n0 2\n0000000000 65535 q \ntrailer << /Size 2 >>\nstartxref\n9\n%%EOF\n"),
		"dict missing val": []byte("%PDF-1.7\n5 0 obj\n<< /Foo >>\nendobj\nstartxref\n0\n%%EOF\n"),
		"unbalanced":       []byte("%PDF-1.7\n1 0 obj << /A [ << /B (x >> endobj\nstartxref\n0\n%%EOF\n"),
	}
	for name, data := range cases {
		f, err := Parse(data)
		if err == nil {
			if _, err := f.Bytes(); err == nil {
				t.Errorf("%s: accepted broken input", name)
			}
		}
	}
}

func TestStringAndNameRoundTrip(t *testing.T) {
	cases := []struct {
		src   string
		check func(Object) bool
	}{
		{"<< /A (a\\(b\\)c) >>", func(o Object) bool { return String("a(b)c") == o.(*Dict).Get("A") }},
		{"<< /A (tab\\tnew\\n1) >>", func(o Object) bool { return String("tab\tnew\n1") == o.(*Dict).Get("A") }},
		{"<< /A (oct\\101\\1012) >>", func(o Object) bool { return String("octAA2") == o.(*Dict).Get("A") }},
		{"<< /A <feff004100e9> >>", func(o Object) bool {
			s, ok := o.(*Dict).Get("A").(String)
			return ok && s == String("\xfe\xff\x00\x41\x00\xe9")
		}},
		{"<< /A (multi\nline) >>", func(o Object) bool { return String("multi\nline") == o.(*Dict).Get("A") }},
		{"<< /B /na#6De >>", func(o Object) bool { return Name("name") == o.(*Dict).Get("B") }},
		{"<< /C [1 -2 3.5 1 0 R null true false] >>", func(o Object) bool {
			a := o.(*Dict).Get("C").(Array)
			return len(a) == 7 && a[2] == Real(3.5) && a[3] == Ref{1, 0} && a[5] == Bool(true)
		}},
	}
	for _, c := range cases {
		f, err := Parse(embedObject(c.src))
		if err != nil {
			t.Errorf("%s: %v", c.src, err)
			continue
		}
		d, ok := f.Object(2).(*Dict)
		if !ok {
			t.Errorf("%s: got %T", c.src, f.Object(2))
			continue
		}
		if !c.check(d) {
			t.Errorf("%s: parsed as %#v", c.src, d)
		}
	}
	// A delimiter ends a name token, so "(b)c" is left dangling and the
	// dictionary is malformed.
	if _, err := Parse(embedObject("<< /B /a(b)c >>")); err == nil {
		t.Errorf("/a(b)c: accepted a dangling token after a name")
	}
}

// embedObject puts src into object 2 of an otherwise trivial document.
func embedObject(src string) []byte {
	objs := []string{
		"<< /Type /Catalog /P 2 0 R >>",
		src,
	}
	return buildClassic2("1.5", objs, "")
}

func TestFilters(t *testing.T) {
	flate := func(b []byte) []byte {
		var z bytes.Buffer
		w := zlib.NewWriter(&z)
		w.Write(b)
		w.Close()
		return z.Bytes()
	}
	flat := flate([]byte("1 0 0 1 0 0 cm\n"))
	// PNG (predictor 15) rows of two columns: row 0 none, row 1 Up.
	pred := flate([]byte{0, 'a', 'b', 2, byte('c' - 'a'), byte('d' - 'b')})
	hexBody := "41 42 4344>"
	a85Body := "<~@:E_W~>"

	objs := []string{
		"<< /Type /Catalog /A 2 0 R /B 3 0 R /C 4 0 R /D 5 0 R >>",
		"<< /Length " + fmt.Sprint(len(flat)) + " /Filter /FlateDecode >>\nstream\n" + string(flat) + "\nendstream",
		"<< /Length " + fmt.Sprint(len(hexBody)) + " /Filter /ASCIIHexDecode >>\nstream\n" + hexBody + "\nendstream",
		"<< /Length " + fmt.Sprint(len(a85Body)) + " /Filter /ASCII85Decode >>\nstream\n" + a85Body + "\nendstream",
		"<< /Length " + fmt.Sprint(len(pred)) + " /Filter /FlateDecode /DecodeParms << /Predictor 15 /Columns 2 >> >>\nstream\n" + string(pred) + "\nendstream",
	}
	f, err := Parse(buildClassic2("1.5", objs, ""))
	if err != nil {
		t.Fatal(err)
	}
	cat, _ := f.Root()
	for i, tc := range []struct {
		key  string
		want string
	}{{"A", "1 0 0 1 0 0 cm\n"}, {"B", "ABCD"}, {"C", "abcd"}, {"D", "abcd"}} {
		s := cat.streamValue(tc.key, f)
		if s == nil {
			t.Fatalf("%s: no stream", tc.key)
		}
		b, err := f.decodeStream(s)
		if err != nil || string(b) != tc.want {
			t.Errorf("stream %d (%s): %q, %v; want %q", i+2, tc.key, b, err, tc.want)
		}
	}
}

func hexDumps(b []byte) string {
	return hex.EncodeToString(b[:min(len(b), 200)])
}

// buildXrefStream writes a PDF whose cross-reference section is an XRef
// stream and whose catalog and page tree live in an object stream.
func buildXrefStream(t *testing.T) []byte {
	t.Helper()
	// Object stream body.
	o1 := "<< /Type /Catalog /Pages 2 0 R >>"
	o2 := "<< /Type /Pages /Kids [4 0 R] /Count 1 >>"
	hdr := fmt.Sprintf("1 0 2 %d ", len(o1))
	body := []byte(hdr + o1 + o2)
	var comp bytes.Buffer
	w := zlib.NewWriter(&comp)
	if _, err := w.Write(body); err != nil {
		t.Fatal(err)
	}
	w.Close()
	stm := comp.Bytes()

	content := []byte("1 0 12 m f")
	var cbuf bytes.Buffer
	cw := zlib.NewWriter(&cbuf)
	cw.Write(content)
	cw.Close()

	var out bytes.Buffer
	out.WriteString("%PDF-1.5\n")
	offs := map[int]int{}
	// object 3: object stream
	offs[3] = out.Len()
	fmt.Fprintf(&out, "3 0 obj\n<< /Type /ObjStm /N 2 /First %d /Length %d /Filter /FlateDecode >>\nstream\n", len(hdr), len(stm))
	out.Write(stm)
	out.WriteString("\nendstream\nendobj\n")
	// object 4: page
	offs[4] = out.Len()
	fmt.Fprintf(&out, "4 0 obj\n<< /Type /Page /Parent 2 0 R /MediaBox [0 0 595 842] /Contents 5 0 R >>\nendobj\n")
	// object 5: content stream
	offs[5] = out.Len()
	fmt.Fprintf(&out, "5 0 obj\n<< /Length %d /Filter /FlateDecode >>\nstream\n", len(cbuf.Bytes()))
	out.Write(cbuf.Bytes())
	out.WriteString("\nendstream\nendobj\n")
	// object 6: page parent entry for object 2 lives in the stream; object 6 is
	// the XRef stream itself.
	xrefPos := out.Len()
	entries := []struct {
		typ byte
		f2  int
		f3  int
	}{
		{0, 0, 0},       // object 0 is free
		{2, 3, 0},       // catalog lives in the object stream
		{2, 3, 1},       // page tree likewise
		{1, offs[3], 0}, // the object stream itself
		{1, offs[4], 0}, // page
		{1, offs[5], 0}, // content stream
		{1, xrefPos, 0}, // this cross-reference stream
	}
	var xb []byte
	for _, e := range entries {
		xb = append(xb, e.typ, byte(e.f2>>8), byte(e.f2), byte(e.f3))
	}
	var xc bytes.Buffer
	xw := zlib.NewWriter(&xc)
	xw.Write(xb)
	xw.Close()
	offs[6] = xrefPos
	fmt.Fprintf(&out, "6 0 obj\n<< /Type /XRef /Size 7 /W [1 2 1] /Root 1 0 R /Filter /FlateDecode /Length %d >>\nstream\n", len(xc.Bytes()))
	out.Write(xc.Bytes())
	out.WriteString("\nendstream\nendobj\n")
	fmt.Fprintf(&out, "startxref\n%d\n%%%%EOF\n", xrefPos)
	return out.Bytes()
}

// TestIndirectStreamLength covers a stream whose /Length is an indirect
// reference. Producers that compress the object stream (Ghostscript,
// LibreOffice, Word) do this routinely, and the integer can sit anywhere in
// the file, including inside an object stream, so the loader must not assume
// the referenced object has been parsed when the stream is.
func TestIndirectStreamLength(t *testing.T) {
	content := "BT /F1 12 Tf 72 720 Td (Hello) Tj ET"
	for name, length := range map[string]string{
		"indirect":         "5 0 R",
		"nonexistent ref":  "99 0 R",
		"length too small": fmt.Sprint(len(content) - 3),
		"length too large": fmt.Sprint(len(content) + 30),
		"negative":         "-5",
	} {
		t.Run(name, func(t *testing.T) {
			objs := []string{
				"<< /Type /Catalog /Pages 2 0 R >>",
				"<< /Type /Pages /Kids [3 0 R] /Count 1 >>",
				"<< /Type /Page /Parent 2 0 R /MediaBox [0 0 595 842] /Contents 4 0 R >>",
				"<< /Length " + length + " >>\nstream\n" + content + "\nendstream",
				fmt.Sprint(len(content)),
			}
			f, err := Parse(buildClassic2("1.4", objs, ""))
			if err != nil {
				t.Fatalf("Parse: %v", err)
			}
			s, ok := f.Object(4).(*Stream)
			if !ok {
				t.Fatalf("object 4 is %T, not a stream", f.Object(4))
			}
			got, err := f.decodeStream(s)
			if err != nil {
				t.Fatalf("Decode: %v", err)
			}
			if string(got) != content {
				t.Errorf("stream body = %q, want %q", got, content)
			}
		})
	}
}
