package pdf

import (
	"bytes"
	"encoding/binary"
	"math"
	"strings"
	"testing"
)

// The ICC profile this package generates is the only colour information an
// invoice PDF carries, and nothing in the Go code reads it back. So the tests
// read it the way a colour engine would: header, tag table, then the numbers,
// checking them against the identities the profile has to satisfy rather than
// against a copy of itself.

func fixed32(b []byte, off int) float64 {
	return float64(int32(binary.BigEndian.Uint32(b[off:]))) / 65536
}

// iccTagTable returns the tag table in table order.
func iccTagTable(t *testing.T, p []byte) []iccTag {
	t.Helper()
	if len(p) < 132 {
		t.Fatalf("profile is %d bytes, too short for a header", len(p))
	}
	n := int(binary.BigEndian.Uint32(p[128:]))
	if n == 0 || 132+12*n > len(p) {
		t.Fatalf("tag count %d does not fit the profile", n)
	}
	out := make([]iccTag, 0, n)
	for i := range n {
		o := 132 + 12*i
		out = append(out, iccTag{
			sig:    string(p[o : o+4]),
			offset: int(binary.BigEndian.Uint32(p[o+4:])),
			length: int(binary.BigEndian.Uint32(p[o+8:])),
		})
	}
	return out
}

type iccTag struct {
	sig    string
	offset int
	length int
}

// iccTagOf finds one tag by signature and returns its data and file offset.
func iccTagOf(p []byte, sig string) ([]byte, int, bool) {
	n := int(binary.BigEndian.Uint32(p[128:]))
	for i := range n {
		o := 132 + 12*i
		if string(p[o:o+4]) != sig {
			continue
		}
		off := int(binary.BigEndian.Uint32(p[o+4:]))
		ln := int(binary.BigEndian.Uint32(p[o+8:]))
		if off < 0 || ln < 0 || off+ln > len(p) {
			return nil, 0, false
		}
		return p[off : off+ln], off, true
	}
	return nil, 0, false
}

func TestSRGBProfileDescription(t *testing.T) {
	p := SRGBProfile()
	data, _, ok := iccTagOf(p, "desc")
	if !ok {
		t.Fatal("profile has no desc tag")
	}
	// ICC.1:2022 clause 8.4.2: signature, ASCII count, reserved, Unicode
	// count, reserved, reserved, then the ASCII text and a Script code name.
	if len(data) < 31 || string(data[:4]) != "desc" {
		t.Fatalf("desc tag is %d bytes and starts %q", len(data), string(data[:min(4, len(data))]))
	}
	ascii := int(binary.BigEndian.Uint32(data[4:]))
	if unicode := binary.BigEndian.Uint32(data[12:]); unicode != 0 {
		t.Errorf("Unicode count is %d, want 0 because no Unicode text is stored", unicode)
	}
	for _, off := range []int{8, 16, 20} {
		if v := binary.BigEndian.Uint32(data[off:]); v != 0 {
			t.Errorf("reserved field at %d is %d, want 0", off, v)
		}
	}
	if 24+ascii+6 > len(data) {
		t.Fatalf("desc tag holds %d ASCII bytes in %d bytes", ascii, len(data))
	}
	if data[23+ascii] != 0 {
		t.Errorf("the ASCII text is not NUL-terminated")
	}
	text := string(data[24 : 23+ascii])
	if !strings.HasPrefix(text, "sRGB") {
		t.Errorf("description is %q, want it to name the colour space", text)
	}
	// Only the empty Script code name and the alignment padding follow, and
	// every byte of them has to be zero.
	for i, b := range data[24+ascii:] {
		if b != 0 {
			t.Fatalf("byte %d after the ASCII text is %#02x, want 0", 24+ascii+i, b)
		}
	}
}

func TestSRGBProfileCopyright(t *testing.T) {
	p := SRGBProfile()
	data, _, ok := iccTagOf(p, "cprt")
	if !ok {
		t.Fatal("profile has no cprt tag")
	}
	// A textType is a signature, a reserved field and a NUL-terminated
	// string; it carries no length prefix.
	if len(data) < 10 || string(data[:4]) != "text" {
		t.Fatalf("cprt tag starts %q", string(data[:min(4, len(data))]))
	}
	for off := range 4 {
		if data[4+off] != 0 {
			t.Errorf("byte at %d is %#02x, want the reserved field to be zero", 4+off, data[4+off])
		}
	}
	text := string(data[8:])
	if end := strings.IndexByte(text, 0); end >= 0 {
		text = text[:end]
	}
	if !strings.Contains(text, "Public domain") {
		t.Errorf("copyright text is %q, want it to state the licence", text)
	}
}

func TestSRGBProfileTagTable(t *testing.T) {
	p := SRGBProfile()
	tags := iccTagTable(t, p)
	for i, tg := range tags {
		if tg.offset < 132+12*len(tags) || tg.offset+tg.length > len(p) {
			t.Errorf("tag %s at %d length %d falls outside the tag data", tg.sig, tg.offset, tg.length)
		}
		if tg.offset%4 != 0 || tg.length%4 != 0 {
			t.Errorf("tag %s at %d with length %d is not aligned to 4 bytes", tg.sig, tg.offset, tg.length)
		}
		if i > 0 && tags[i-1].sig >= tg.sig {
			t.Errorf("tag table not sorted by signature: %s then %s", tags[i-1].sig, tg.sig)
		}
	}
	// ICC requires these tags in an RGB display profile.
	for _, sig := range []string{"desc", "wtpt", "bkpt", "rXYZ", "gXYZ", "bXYZ", "rTRC", "gTRC", "bTRC"} {
		if _, _, ok := iccTagOf(p, sig); !ok {
			t.Errorf("profile has no %s tag", sig)
		}
	}
	// Each tag's data starts with the type it is meant to hold.
	types := map[string]string{"wtpt": "XYZ ", "bkpt": "XYZ ", "rXYZ": "XYZ ", "gXYZ": "XYZ ",
		"bXYZ": "XYZ ", "rTRC": "curv", "gTRC": "curv", "bTRC": "curv", "desc": "desc",
		"cprt": "text", "chad": "sf32"}
	for sig, want := range types {
		data, _, ok := iccTagOf(p, sig)
		if !ok {
			continue
		}
		if len(data) < 8 || string(data[:4]) != want {
			t.Errorf("%s tag holds %q, want a %s structure", sig, string(data[:min(4, len(data))]), want)
		}
	}
	// The three tone curves are identical, so they must be stored once.
	r, ro, _ := iccTagOf(p, "rTRC")
	b, bo, _ := iccTagOf(p, "bTRC")
	if len(r) == 0 || len(b) == 0 {
		t.Fatal("missing tone curve")
	}
	if ro != bo {
		t.Errorf("identical tone curves stored twice: rTRC at %d, bTRC at %d", ro, bo)
	}
}

// TestSRGBProfilePrimaries checks the colourimetry: the primaries are those of
// IEC 61966-2-1 after adaptation to the D50 PCS, the columns of the matrix have
// to add up to the profile's white point, and the chad tag has to be the matrix
// that makes that true.
func TestSRGBProfilePrimaries(t *testing.T) {
	p := SRGBProfile()
	col := func(sig string) [3]float64 {
		data, _, ok := iccTagOf(p, sig)
		if !ok || len(data) != 20 {
			t.Fatalf("%s tag missing or %d bytes", sig, len(data))
		}
		return [3]float64{fixed32(data, 8), fixed32(data, 12), fixed32(data, 16)}
	}
	r, g, b, w := col("rXYZ"), col("gXYZ"), col("bXYZ"), col("wtpt")
	// Published D50 values for sRGB (ICC reference profile). The blue and
	// green Z differ a little because that profile rounded its adaptation
	// matrix differently; 0.006 covers it without hiding a wrong matrix.
	ref := map[string][3]float64{
		"rXYZ": {0.43607, 0.22250, 0.01392},
		"gXYZ": {0.38503, 0.71688, 0.09215},
		"bXYZ": {0.14307, 0.06060, 0.71412},
	}
	got := map[string][3]float64{"rXYZ": r, "gXYZ": g, "bXYZ": b}
	for sig, want := range ref {
		for i := range 3 {
			if math.Abs(got[sig][i]-want[i]) > 0.006 {
				t.Errorf("%s component %d is %.5f, want %.5f (±0.006)", sig, i, got[sig][i], want[i])
			}
		}
	}
	// White is the sum of the primaries: the identity every RGB profile relies on.
	for i := range 3 {
		sum := r[i] + g[i] + b[i]
		if math.Abs(sum-w[i]) > 1e-4 {
			t.Errorf("primaries sum to %.5f in component %d, white point says %.5f", sum, i, w[i])
		}
	}
	if bk := col("bkpt"); bk != [3]float64{0, 0, 0} {
		t.Errorf("media black point %v, want all zero", bk)
	}
	// chad must adapt the D65 white of the emissive primaries to the D50 PCS.
	data, _, ok := iccTagOf(p, "chad")
	if !ok || len(data) != 44 {
		t.Fatalf("chad tag missing or %d bytes, want 44", len(data))
	}
	m := [3][3]float64{}
	for row := range 3 {
		for c := range 3 {
			m[row][c] = fixed32(data, 8+4*(row*3+c))
		}
	}
	d65 := [3]float64{0.950456, 1, 1.089058} // D65 as XYZ
	for row := range 3 {
		got := m[row][0]*d65[0] + m[row][1]*d65[1] + m[row][2]*d65[2]
		if math.Abs(got-w[row]) > 5e-4 {
			t.Errorf("chad takes D65 white to %.5f in component %d, white point says %.5f", got, row, w[row])
		}
	}
}

func TestSRGBProfileToneCurve(t *testing.T) {
	p := SRGBProfile()
	data, _, ok := iccTagOf(p, "rTRC")
	if !ok {
		t.Fatal("no rTRC tag")
	}
	n := int(binary.BigEndian.Uint32(data[4:]))
	if n != 1024 || len(data) != 8+2*n {
		t.Fatalf("tone curve has %d entries in %d bytes, want 1024 in %d", n, len(data), 8+2*n)
	}
	prev := -1
	for i := range n {
		v := int(binary.BigEndian.Uint16(data[8+2*i:]))
		if v < prev {
			t.Fatalf("tone curve not monotonic at entry %d: %d after %d", i, v, prev)
		}
		prev = v
	}
	if first := binary.BigEndian.Uint16(data[8:]); first != 0 {
		t.Errorf("first entry %d, want 0", first)
	}
	if last := binary.BigEndian.Uint16(data[8+2*(n-1):]); last != 65535 {
		t.Errorf("last entry %d, want 65535", last)
	}
	// The curve is the exact companding function, sampled.
	for _, i := range []int{0, 1, 10, 100, 255, 512, 900, 1023} {
		got := float64(binary.BigEndian.Uint16(data[8+2*i:])) / 65535
		want := sRGBLinearize(float64(i) / float64(n-1))
		if math.Abs(got-want) > 1.5/65535 {
			t.Errorf("entry %d is %.6f, want %.6f", i, got, want)
		}
	}
}

func TestSRGBProfileIsReproducible(t *testing.T) {
	first := SRGBProfile()
	second := sRGBProfile()
	if !bytes.Equal(first, second) {
		t.Fatal("two runs produced different profiles")
	}
	if !bytes.Equal(SRGBProfile(), first) {
		t.Fatal("SRGBProfile and sRGBProfile disagree")
	}
	// Any change to the numbers a colour engine reads has to be deliberate,
	// because it silently changes how every hybrid invoice renders.
	if got := SRGBProfileMD5(); got != "35d290bcd32b5f307066a5158779565c" {
		t.Errorf("profile digest changed: %s", got)
	}
}

func TestSRGBLinearize(t *testing.T) {
	for _, tc := range []struct{ in, want float64 }{
		{0, 0},
		{0.04045, 0.04045 / 12.92},     // the linear segment, inclusive
		{0.0031308, 0.0031308 / 12.92}, // mid linear segment
		{0.5, math.Pow((0.5+0.055)/1.055, 2.4)},
		{1, 1},
	} {
		if got := sRGBLinearize(tc.in); math.Abs(got-tc.want) > 1e-12 {
			t.Errorf("sRGBLinearize(%v) = %v, want %v", tc.in, got, tc.want)
		}
	}
}

func TestTagBuilders(t *testing.T) {
	xyz := xyzTag([3]float64{0.9505, 1, -0.0888})
	if string(xyz[:4]) != "XYZ " || binary.BigEndian.Uint32(xyz[4:]) != 0 || len(xyz) != 20 {
		t.Errorf("xyzTag produced % x", xyz)
	}
	if got := fixed32(xyz, 16); math.Abs(got+0.0888) > 1e-4 {
		t.Errorf("negative component encoded as %.4f", got)
	}
	sf := s15Tag([3][3]float64{{1, 0, 0}, {0, -1, 0}, {0, 0, 2}})
	if string(sf[:4]) != "sf32" || len(sf) != 44 || binary.BigEndian.Uint32(sf[4:]) != 0 {
		t.Errorf("s15Tag produced %d bytes starting %q", len(sf), sf[:4])
	}
	if got := fixed32(sf, 8); math.Abs(got-1) > 1e-6 {
		t.Errorf("first matrix element %.4f, want 1", got)
	}
	if got := fixed32(sf, 24); math.Abs(got+1) > 1e-6 {
		t.Errorf("centre matrix element %.4f, want -1", got)
	}
	desc := descriptionTag("sRGB")
	// sig(4) + ASCII count(4) + reserved(4) + Unicode count(4) +
	// reserved(4) + reserved(4) + "sRGB" + NUL + Script code name(6)
	if string(desc[:4]) != "desc" || len(desc) != 24+5+6 {
		t.Fatalf("descriptionTag produced %d bytes: %v", len(desc), desc[:12])
	}
	if got := binary.BigEndian.Uint32(desc[4:]); got != 5 {
		t.Errorf("ASCII count is %d, want 5 (the text plus its NUL)", got)
	}
	if got := binary.BigEndian.Uint32(desc[12:]); got != 0 {
		t.Errorf("Unicode count is %d, want 0 (no Unicode text)", got)
	}
	if string(desc[24:len(desc)-6]) != "sRGB\x00" {
		t.Errorf("description text is %q, want sRGB and a NUL", desc[24:len(desc)-6])
	}
	txt := textTag("hi")
	if string(txt[:4]) != "text" || len(txt) != 11 || string(txt[8:]) != "hi\x00" {
		t.Errorf("textTag produced %q", txt)
	}
}

func TestSortTags(t *testing.T) {
	tags := []struct {
		sig  string
		data []byte
	}{{"wtpt", nil}, {"bTRC", nil}, {"desc", nil}, {"bkpt", nil}}
	sortTags(tags)
	var got []string
	for _, tg := range tags {
		got = append(got, tg.sig)
	}
	want := []string{"bTRC", "bkpt", "desc", "wtpt"}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("sorted to %v, want %v", got, want)
		}
	}
}

func TestLinearAlgebraHelpers(t *testing.T) {
	m := [3][3]float64{{2, 1, 0}, {0, 3, 1}, {4, 0, 5}} // column-wise
	v := [3]float64{3, 4, 9}
	x := solve3(m, v)
	for r := range 3 {
		got := m[0][r]*x[0] + m[1][r]*x[1] + m[2][r]*x[2]
		if math.Abs(got-v[r]) > 1e-9 {
			t.Errorf("solve3 gave %v, m*x = %v in row %d, want %v", x, got, r, v[r])
		}
	}
	b := [3][3]float64{{0.8951, 0.2664, -0.1614}, {-0.7502, 1.7135, 0.0367}, {0.0389, -0.0685, 1.0296}}
	id := matMul(matInv(b), b)
	for r := range 3 {
		for c := range 3 {
			want := 0.0
			if r == c {
				want = 1
			}
			if math.Abs(id[r][c]-want) > 1e-9 {
				t.Errorf("inv(b)*b is %.9f at %d,%d, want %.0f", id[r][c], r, c, want)
			}
		}
	}
	if got := matMulVec(b, [3]float64{1, 1, 1}); math.Abs(got[1]-1.0) > 1e-9 {
		t.Errorf("matMulVec row 1 = %v, want 1", got[1])
	}
	if p := pivotRow([3][4]float64{{1, 0, 0, 0}, {0, -9, 0, 0}, {0, 2, 0, 0}}, 1); p != 1 {
		t.Errorf("pivotRow chose row %d, want 1", p)
	}
	if p := pivotRow([3][4]float64{{1, 0, 0, 0}, {0, -9, 0, 0}, {0, 2, 0, 0}}, 0); p != 0 {
		t.Errorf("pivotRow chose row %d, want 0", p)
	}
}

func TestAdaptPreservesWhite(t *testing.T) {
	bradford := [3][3]float64{
		{0.8951, 0.2664, -0.1614},
		{-0.7502, 1.7135, 0.0367},
		{0.0389, -0.0685, 1.0296},
	}
	d65 := [3]float64{0.950456, 1, 1.089058}
	d50 := [3]float64{0.9642, 1, 0.8249}
	a := adapt(bradford, d65, d50)
	for r := range 3 {
		got := a[r][0]*d65[0] + a[r][1]*d65[1] + a[r][2]*d65[2]
		if math.Abs(got-d50[r]) > 1e-9 {
			t.Errorf("adaptation sends D65 to %.6f in component %d, want %.4f", got, r, d50[r])
		}
	}
	// Adapting a white to itself is the identity.
	same := adapt(bradford, d50, d50)
	for r := range 3 {
		for c := range 3 {
			want := 0.0
			if r == c {
				want = 1
			}
			if math.Abs(same[r][c]-want) > 1e-9 {
				t.Errorf("self-adaptation is %.9f at %d,%d, want %.0f", same[r][c], r, c, want)
			}
		}
	}
}

func TestSingularMatricesPanic(t *testing.T) {
	for _, tc := range []struct {
		name string
		run  func()
	}{
		{"solve3", func() { solve3([3][3]float64{{1, 2, 3}, {2, 4, 6}, {0, 0, 1}}, [3]float64{1, 2, 3}) }},
		{"matInv", func() { matInv([3][3]float64{{1, 2, 3}, {2, 4, 6}, {0, 0, 1}}) }},
	} {
		if !panics(tc.run) {
			t.Errorf("%s accepted a singular matrix", tc.name)
		}
	}
}

func panics(run func()) (p bool) {
	defer func() {
		if recover() != nil {
			p = true
		}
	}()
	run()
	return false
}
