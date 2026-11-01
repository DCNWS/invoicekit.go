package pdf

import (
	"bytes"
	"errors"
	"testing"
)

// The row predictors decide what an embedded invoice XML or an ICC profile
// looks like after decoding, and a wrong predictor silently produces garbage
// bytes rather than an error. These tests encode a small image with each
// filter from the spec and check that applyPredictor turns it back.

// img is the sample: two rows of four RGB pixels, with values that wrap past
// 255 once differenced so an off-by-one in the arithmetic shows up.
var img = [][]byte{
	{0, 200, 250, 255, 12, 1, 200, 199, 3, 250, 255, 128},
	{250, 5, 100, 99, 255, 200, 1, 2, 130, 129, 0, 255},
}

const (
	predCols   = 4
	predComps  = 3
	predBPC    = 8
	predRowLen = predCols * predComps
	predBPP    = predComps
)

func predDict(kv ...Object) *Dict {
	base := []Object{
		Name("Columns"), Int(predCols),
		Name("BitsPerComponent"), Int(predBPC),
		Name("Colors"), Int(predComps),
	}
	return DictFrom(append([]Object{Name("Predictor")}, append(kv, base...)...)...)
}

// encodePNG writes every row through filter ft, then prefixes each row with
// its filter byte, which is the wire form predictors 10 to 14 use.
func encodePNG(rows [][]byte, ft byte) []byte {
	return encodeRows(rows, bytes.Repeat([]byte{ft}, len(rows)))
}

// encodeRows encodes every row with its own filter byte while carrying prev
// across rows, so the Up and Paeth filters see the real row above. Splitting a
// stream into separate encodePNG calls would reset that state and mis-encode
// everything after the first row.
func encodeRows(rows [][]byte, fts []byte) []byte {
	var out []byte
	var prev []byte
	for n, row := range rows {
		ft := fts[n]
		out = append(out, ft)
		for i := range row {
			l := byte(0)
			if i >= predBPP {
				l = row[i-predBPP]
			}
			a := byte(0)
			if prev != nil {
				a = prev[i]
			}
			c := byte(0)
			if prev != nil && i >= predBPP {
				c = prev[i-predBPP]
			}
			switch ft {
			case 0:
				out = append(out, row[i])
			case 1:
				out = append(out, row[i]-l)
			case 2:
				out = append(out, row[i]-a)
			case 3:
				out = append(out, row[i]-byte((uint16(l)+uint16(a))/2))
			case 4:
				out = append(out, row[i]-paethGuess(l, a, c))
			}
		}
		prev = row
	}
	return out
}

// paethGuess is the encoder side of the Paeth filter: the neighbour closest to
// the linear prediction l+a-c, where l is the left, a the above and c the
// upper-left byte.
func paethGuess(l, a, c byte) byte {
	p := int16(l) + int16(a) - int16(c)
	pa, pb, pc := abs16(p-int16(l)), abs16(p-int16(a)), abs16(p-int16(c))
	switch {
	case pa <= pb && pa <= pc:
		return l
	case pb <= pc:
		return a
	default:
		return c
	}
}

func abs16(v int16) int16 {
	if v < 0 {
		return -v
	}
	return v
}

func flat(rows [][]byte) []byte {
	var out []byte
	for _, r := range rows {
		out = append(out, r...)
	}
	return out
}

func TestPredictorUndoesEachPNGFilter(t *testing.T) {
	want := flat(img)
	for _, ft := range []byte{0, 1, 2, 3, 4} {
		p := predDict(Int(10 + int(ft)))
		got, err := applyPredictor(encodePNG(img, ft), p)
		if err != nil {
			t.Fatalf("predictor %d: %v", 10+ft, err)
		}
		if !bytes.Equal(got, want) {
			t.Errorf("predictor %d = %v, want %v", 10+ft, got, want)
		}
	}
}

// TestPaethPicksUpperLeft covers the branch of the Paeth rule where neither the
// left nor the above byte is nearest to the linear prediction.
func TestPaethPicksUpperLeft(t *testing.T) {
	prev := []byte{60, 10}
	row := []byte{40, 226}
	pngPaeth(row, prev, 1)
	// p = 100 + 10 - 60 = 50, so |p-left| is 50, |p-above| is 40 and
	// |p-upper-left| is 10: the upper-left neighbour wins.
	if want := []byte{100, 30}; !bytes.Equal(row, want) {
		t.Errorf("row = %v, want %v", row, want)
	}
}

func TestPredictor15ReadsTheFilterPerRow(t *testing.T) {
	// Each row carries its own filter byte, so the stream is built row by row.
	data := encodeRows(img, []byte{1, 2})
	p := predDict(Int(15))
	got, err := applyPredictor(data, p)
	if err != nil {
		t.Fatal(err)
	}
	if want := flat(img); !bytes.Equal(got, want) {
		t.Errorf("predictor 15 = %v, want %v", got, want)
	}
}

func TestTIFFPredictorUndoesRowDifferencing(t *testing.T) {
	var data []byte
	for _, row := range img {
		for i, v := range row {
			if i >= predBPP {
				v -= row[i-predBPP]
			}
			data = append(data, v)
		}
	}
	p := predDict(Int(2))
	got, err := applyPredictor(data, p)
	if err != nil {
		t.Fatal(err)
	}
	if want := flat(img); !bytes.Equal(got, want) {
		t.Errorf("predictor 2 = %v, want %v", got, want)
	}
}

func TestPredictor1LeavesTheStreamAlone(t *testing.T) {
	p := predDict(Int(1))
	got, err := applyPredictor([]byte("raw bytes"), p)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "raw bytes" {
		t.Errorf("predictor 1 changed the data: %q", got)
	}
}

func TestPredictorErrors(t *testing.T) {
	valid := encodePNG(img, 1)
	for _, tc := range []struct {
		name string
		data []byte
		p    *Dict
		want string
	}{
		{"no columns", valid, DictFrom(Name("Predictor"), Int(12)), "without /Columns"},
		{"zero columns", valid, DictFrom(Name("Predictor"), Int(12), Name("Columns"), Int(0)), "without /Columns"},
		{"unknown predictor", valid, predDict(Int(99)), "predictor 99"},
		{"unknown png filter", append([]byte{9}, valid[1:]...), predDict(Int(12)), "unknown PNG predictor 9"},
		{"short row", valid[:3], predDict(Int(12)), "row truncated"},
		{"tiff at four bits", valid, DictFrom(Name("Predictor"), Int(2), Name("Columns"), Int(predCols), Name("BitsPerComponent"), Int(4), Name("Colors"), Int(predComps)), "4 bits per component"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := applyPredictor(tc.data, tc.p)
			if err == nil {
				t.Fatalf("want an error mentioning %q", tc.want)
			}
			if !bytes.Contains([]byte(err.Error()), []byte(tc.want)) {
				t.Errorf("error = %q, want it to mention %q", err, tc.want)
			}
		})
	}
}

func TestTIFFPredictorIsUnsupportedAtFourBits(t *testing.T) {
	p := DictFrom(Name("Predictor"), Int(2), Name("Columns"), Int(predCols),
		Name("BitsPerComponent"), Int(4), Name("Colors"), Int(predComps))
	_, err := applyPredictor(validRows(), p)
	if !errors.Is(err, ErrUnsupported) {
		t.Errorf("err = %v, want ErrUnsupported", err)
	}
}

func validRows() []byte { return encodePNG(img, 0) }
