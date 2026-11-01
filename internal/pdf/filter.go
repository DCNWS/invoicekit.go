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
	if filters.len() == 0 && len(parms) > 0 {
		if p := parms[0]; p != nil && p.Has("Predictor") {
			return applyPredictor(out, p)
		}
	}
	return out, nil
}

type nameList []string

func (n nameList) len() int { return len(n) }

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
	var acc uint32
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
		acc = acc*85 + uint32(c-'!')
		n++
		if n == 5 {
			out = append(out, byte(acc>>24), byte(acc>>16), byte(acc>>8), byte(acc))
			acc, n = 0, 0
		}
	}
	if n > 1 {
		for range 5 - n {
			acc *= 85
		}
		for i := range n - 1 {
			out = append(out, byte(acc>>uint(24-8*i)))
		}
	}
	return out, nil
}

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
			out = append(out, data[i+1:i+1+length]...)
			i += length + 1
			continue
		}
		if i >= len(data) {
			return nil, fmt.Errorf("truncated runlength data")
		}
		for k := 0; k <= 128-length; k++ {
			out = append(out, data[i])
		}
		i++
	}
	return out, nil
}

// applyPredictor undoes a row predictor in a decoded stream.
func applyPredictor(data []byte, p *Dict) ([]byte, error) {
	pred, _ := p.IntValue("Predictor")
	if pred == 1 {
		return data, nil
	}
	cols64, ok := p.IntValue("Columns")
	if !ok || cols64 <= 0 {
		return nil, fmt.Errorf("pdf: predictor without /Columns")
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
	rowBits := cols * comps * bpc
	rowBytes := int((rowBits + 7) / 8)
	switch pred {
	case 10, 11, 12, 13, 14, 15: // PNG predictors; 15 chooses per row
		out := make([]byte, 0, len(data))
		var prev []byte
		for pos := 0; pos < len(data); {
			ft := data[pos]
			pos++
			if pos+rowBytes > len(data) {
				return nil, fmt.Errorf("pdf: predictor row truncated")
			}
			row := make([]byte, rowBytes)
			copy(row, data[pos:pos+rowBytes])
			pos += rowBytes
			switch ft {
			case 0:
			case 1:
				bpp := (comps*bpc + 7) / 8
				for i := bpp; i < rowBytes; i++ {
					row[i] += row[i-bpp]
				}
			case 2:
				for i := range rowBytes {
					if i < len(prev) {
						row[i] += prev[i]
					}
				}
			case 3:
				bpp := (comps*bpc + 7) / 8
				for i := range rowBytes {
					a := 0
					if i >= bpp {
						a = int(row[i-bpp])
					}
					b := 0
					if i < len(prev) {
						b = int(prev[i])
					}
					row[i] += byte((a + b) / 2)
				}
			case 4:
				bpp := (comps*bpc + 7) / 8
				for i := range rowBytes {
					a, b, c := 0, 0, 0
					if i >= bpp {
						a = int(row[i-bpp])
					}
					if i < len(prev) {
						b = int(prev[i])
					}
					if i >= bpp && i < len(prev) {
						c = int(prev[i-bpp])
					}
					pa, pb, pc := abs(a-b), abs(b-c), abs(a-c)
					switch {
					case pa <= pb && pa <= pc:
						row[i] += byte(a)
					case pb <= pc:
						row[i] += byte(b)
					default:
						row[i] += byte(c)
					}
				}
			default:
				return nil, fmt.Errorf("pdf: unknown PNG predictor %d", ft)
			}
			out = append(out, row...)
			prev = row
		}
		return out, nil
	case 2: // TIFF differencing (horizontal)
		if bpc != 8 {
			return nil, fmt.Errorf("%w: TIFF predictor with %d bits per component", ErrUnsupported, bpc)
		}
		out := make([]byte, len(data))
		for pos := 0; pos < len(data); pos += rowBytes {
			end := min(pos+rowBytes, len(data))
			bpp := (comps*8 + 7) / 8
			for i := pos; i < end; i++ {
				if i-bpp >= pos {
					out[i] = data[i] + out[i-bpp]
				} else {
					out[i] = data[i]
				}
			}
		}
		return out, nil
	}
	return nil, fmt.Errorf("%w: predictor %d", ErrUnsupported, pred)
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
