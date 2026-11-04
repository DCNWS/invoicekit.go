package pdf

import (
	"bytes"
	"compress/flate"
	"compress/zlib"
	"fmt"
	"io"
)

// decodeStream returns the decoded content of a stream, applying its filters.
// It supports the filters that appear in invoice PDFs: FlateDecode (with
// predictors), ASCIIHexDecode and ASCII85Decode.
func (f *File) decodeStream(s *Stream) ([]byte, error) {
	out := s.Raw
	filters := filterList(s.Dict.Get("Filter"))
	parms := parmList(s.Dict.Get("DecodeParms"), len(filters))
	for i, name := range filters {
		var err error
		switch name {
		case "FlateDecode":
			out, err = inflate(out)
		case "ASCIIHexDecode":
			out, err = asciiHexDecode(out)
		case "ASCII85Decode":
			out, err = ascii85Decode(out)
		case "RunLengthDecode":
			out, err = runLengthDecode(out)
		default:
			return nil, fmt.Errorf("%w: filter %s", ErrUnsupported, name)
		}
		if err != nil {
			return nil, fmt.Errorf("pdf: filter %s: %w", name, err)
		}
		if p := parms[i]; p != nil && p.Has("Predictor") {
			out, err = applyPredictor(out, p)
			if err != nil {
				return nil, err
			}
		}
	}
	return out, nil
}

type nameList []string

func filterList(v Object) nameList {
	switch v := v.(type) {
	case Name:
		return nameList{string(v)}
	case Array:
		var out nameList
		for _, e := range v {
			if n, ok := e.(Name); ok {
				out = append(out, string(n))
			}
		}
		return out
	}
	return nil
}

func parmList(v Object, n int) []*Dict {
	out := make([]*Dict, n)
	if n == 1 {
		if d, ok := v.(*Dict); ok {
			out[0] = d
		}
		return out
	}
	if a, ok := v.(Array); ok {
		for i := range min(n, len(a)) {
			if d, ok := a[i].(*Dict); ok {
				out[i] = d
			}
		}
	}
	return out
}

// inflate accepts zlib-wrapped deflate data and bare deflate streams.
func inflate(data []byte) ([]byte, error) {
	zr, err := zlib.NewReader(bytes.NewReader(data))
	if err == nil {
		out, rerr := io.ReadAll(zr)
		zr.Close()
		if rerr == nil {
			return out, nil
		}
		if len(out) > 0 {
			return out, nil
		}
		err = rerr
	}
	fr := flate.NewReader(bytes.NewReader(data))
	out, rerr := io.ReadAll(fr)
	fr.Close()
	if rerr != nil && len(out) == 0 {
		return nil, fmt.Errorf("inflate: %w (zlib: %v)", rerr, err)
	}
	return out, nil
}

func asciiHexDecode(data []byte) ([]byte, error) {
	var nib []byte
	for _, c := range data {
		if c == '>' {
			break
		}
		if isSpaceByte(c) {
			continue
		}
		v, ok := hexValue(c)
		if !ok {
			return nil, fmt.Errorf("bad hex digit %q", c)
		}
		nib = append(nib, v)
	}
	if len(nib)%2 == 1 {
		nib = append(nib, 0)
	}
	out := make([]byte, len(nib)/2)
	for i := range out {
		out[i] = nib[2*i]<<4 | nib[2*i+1]
	}
	return out, nil
}

func ascii85Decode(data []byte) ([]byte, error) {
	// The PDF ASCII85 alphabet is every printable byte from "!" to "~".
	var out []byte
	var acc uint64
	n := 0
	// Adobe-style delimiters: <~ starts and ~> ends the data.
	i := 0
	for i < len(data) && isSpaceByte(data[i]) {
		i++
	}
	if i+1 < len(data) && data[i] == '<' && data[i+1] == '~' {
		data = data[i+2:]
	}
	for i := range len(data) {
		c := data[i]
		if isSpaceByte(c) {
			continue
		}
		if c == '~' && i+1 < len(data) && data[i+1] == '>' {
			break
		}
		if c == '>' { // end-of-data in the non-Adobe flavour
			break
		}
		if c == 'z' && n == 0 {
			out = append(out, 0, 0, 0, 0)
			continue
		}
		if c < '!' || c > '~' {
			return nil, fmt.Errorf("bad ascii85 character %q", c)
		}
		acc = acc*85 + uint64(c-'!')
		n++
		if n == 5 {
			out = append(out, byte(acc>>24), byte(acc>>16), byte(acc>>8), byte(acc))
			acc, n = 0, 0
		}
	}
	if n > 1 {
		// The writer cut a five-character group short, so the digits that
		// would have followed are unknown. Padding them with the largest
		// digit recovers the leading n-1 bytes exactly; padding with zeros
		// can round a byte down. A padded group can exceed 2^32, hence the
		// wider accumulator here.
		pad := acc
		for range 5 - n {
			pad = pad*85 + 84
		}
		for i := range n - 1 {
			out = append(out, byte(pad>>uint(24-8*i)))
		}
	}
	return out, nil
}

// runLengthDecode undoes the RunLengthDecode filter (ISO 32000-1 7.4.5): a
// length byte of 0..127 introduces length+1 literal bytes, 128 marks the end
// of the data, and 129..254 repeats the byte that follows it 257-length times.
func runLengthDecode(data []byte) ([]byte, error) {
	var out []byte
	for i := 0; i < len(data); {
		length := int(data[i])
		i++
		if length == 128 {
			break
		}
		if length < 128 {
			if i+length+1 > len(data) {
				return nil, fmt.Errorf("truncated runlength data")
			}
			out = append(out, data[i:i+length+1]...)
			i += length + 1
			continue
		}
		if i >= len(data) {
			return nil, fmt.Errorf("truncated runlength data")
		}
		for k := 0; k < 257-length; k++ {
			out = append(out, data[i])
		}
		i++
	}
	return out, nil
}

// predictor is the sample layout a row predictor works on: how wide a row is
// on the wire, how far a pixel extends sideways, and how deep a sample is.
type predictor struct {
	pred     int
	rowBytes int
	bpp      int
	bpc      int
}

func predictorOf(p *Dict) (predictor, error) {
	pred, _ := p.IntValue("Predictor")
	cols64, ok := p.IntValue("Columns")
	if !ok || cols64 <= 0 {
		return predictor{}, fmt.Errorf("pdf: predictor without /Columns")
	}
	bpc64, ok := p.IntValue("BitsPerComponent")
	if !ok || bpc64 <= 0 {
		bpc64 = 8
	}
	comps64, ok := p.IntValue("Colors")
	if !ok || comps64 <= 0 {
		comps64 = 1
	}
	cols, bpc, comps := int(cols64), int(bpc64), int(comps64)
	return predictor{
		pred:     int(pred),
		rowBytes: (cols*comps*bpc + 7) / 8,
		bpp:      (comps*bpc + 7) / 8,
		bpc:      bpc,
	}, nil
}

// applyPredictor undoes a row predictor in a decoded stream.
func applyPredictor(data []byte, p *Dict) ([]byte, error) {
	pr, err := predictorOf(p)
	if err != nil {
		return nil, err
	}
	if pr.pred == 1 {
		return data, nil
	}
	switch pr.pred {
	case 10, 11, 12, 13, 14, 15: // PNG predictors; 15 chooses per row
		return pngRows(data, pr)
	case 2: // TIFF differencing (horizontal)
		return tiffRow(data, pr)
	}
	return nil, fmt.Errorf("%w: predictor %d", ErrUnsupported, pr.pred)
}

// pngRows reads each row's filter byte and applies the matching filter. The
// filters are a table because the five PNG filters are independent of each
// other and the choice between them is per row, not per stream.
func pngRows(data []byte, pr predictor) ([]byte, error) {
	out := make([]byte, 0, len(data))
	var prev []byte
	for pos := 0; pos < len(data); {
		ft := data[pos]
		pos++
		if pos+pr.rowBytes > len(data) {
			return nil, fmt.Errorf("pdf: predictor row truncated")
		}
		row := make([]byte, pr.rowBytes)
		copy(row, data[pos:pos+pr.rowBytes])
		pos += pr.rowBytes
		f, ok := pngFilters[ft]
		if !ok {
			return nil, fmt.Errorf("pdf: unknown PNG predictor %d", ft)
		}
		f(row, prev, pr.bpp)
		out = append(out, row...)
		prev = row
	}
	return out, nil
}

// rowFilter turns one row in place, given the previous row and the width of a
// pixel in bytes.
type rowFilter func(row, prev []byte, bpp int)

var pngFilters = map[byte]rowFilter{
	0: pngNone,
	1: pngSub,
	2: pngUp,
	3: pngAverage,
	4: pngPaeth,
}

func pngNone(row, prev []byte, bpp int) {}

func pngSub(row, prev []byte, bpp int) {
	for i := bpp; i < len(row); i++ {
		row[i] += row[i-bpp]
	}
}

func pngUp(row, prev []byte, bpp int) {
	for i := range row {
		if i < len(prev) {
			row[i] += prev[i]
		}
	}
}

func pngAverage(row, prev []byte, bpp int) {
	for i := range row {
		row[i] += byte((left(row, i, bpp) + above(prev, i)) / 2)
	}
}

// pngPaeth picks whichever of the left, above and upper-left bytes is closest
// to the linear prediction a+b-c.
func pngPaeth(row, prev []byte, bpp int) {
	for i := range row {
		a, b, c := left(row, i, bpp), above(prev, i), upperLeft(prev, i, bpp)
		// Pick the neighbour closest to the linear prediction p = a + b - c.
		p := a + b - c
		pa, pb, pc := abs(p-a), abs(p-b), abs(p-c)
		switch {
		case pa <= pb && pa <= pc:
			row[i] += byte(a)
		case pb <= pc:
			row[i] += byte(b)
		default:
			row[i] += byte(c)
		}
	}
}

// left, above and upperLeft read the neighbouring sample bytes, which are
// taken as zero outside the image.
func left(row []byte, i, bpp int) int {
	if i >= bpp {
		return int(row[i-bpp])
	}
	return 0
}

func above(prev []byte, i int) int {
	if i < len(prev) {
		return int(prev[i])
	}
	return 0
}

func upperLeft(prev []byte, i, bpp int) int {
	if i >= bpp && i-bpp < len(prev) {
		return int(prev[i-bpp])
	}
	return 0
}

// tiffRow undoes the horizontal TIFF differencing predictor, which only makes
// sense on whole bytes.
func tiffRow(data []byte, pr predictor) ([]byte, error) {
	if pr.bpc != 8 {
		return nil, fmt.Errorf("%w: TIFF predictor with %d bits per component", ErrUnsupported, pr.bpc)
	}
	out := make([]byte, len(data))
	for pos := 0; pos < len(data); pos += pr.rowBytes {
		end := min(pos+pr.rowBytes, len(data))
		for i := pos; i < end; i++ {
			if i-pr.bpp >= pos {
				out[i] = data[i] + out[i-pr.bpp]
			} else {
				out[i] = data[i]
			}
		}
	}
	return out, nil
}

func abs(v int) int {
	if v < 0 {
		return -v
	}
	return v
}

// StreamBytes returns the decoded contents of a stream, undoing whatever
// filters its dictionary names. It is the exported form of the decoding the
// reader uses internally, so that callers inspecting a document (an embedded
// invoice XML, an ICC profile, a font file) do not have to reimplement the
// filter chain.
func (f *File) StreamBytes(s *Stream) ([]byte, error) {
	if s == nil {
		return nil, fmt.Errorf("pdf: nil stream")
	}
	return f.decodeStream(s)
}
