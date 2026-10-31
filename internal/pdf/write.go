package pdf

import (
	"bytes"
	"compress/zlib"
	"crypto/md5"
	"fmt"
	"strconv"
)

// Bytes serialises the document. The cross-reference structures are rebuilt
// as a classic table with a plain trailer, which every reader and every PDF/A
// validator accepts, and stream /Length entries are rewritten as direct
// integers.
func (f *File) Bytes() ([]byte, error) {
	if _, err := f.Root(); err != nil {
		return nil, err
	}
	for _, num := range f.nums {
		if s, ok := f.objs[num].(*Stream); ok {
			if s.Dict == nil {
				return nil, fmt.Errorf("pdf: stream object %d has no dictionary", num)
			}
			s.Dict.Set("Length", Int(len(s.Raw)))
		}
	}

	var out bytes.Buffer
	out.WriteString("%PDF-" + f.Version + "\n")
	// A line of high-byte comments marks the file as binary for transport.
	out.WriteString("%\xe2\xe3\xcf\xd3\n")

	offsets := make(map[int]int)
	for _, num := range f.nums {
		offsets[num] = out.Len()
		out.WriteString(strconv.Itoa(num))
		out.WriteString(" 0 obj\n")
		if err := writeValue(&out, f.objs[num]); err != nil {
			return nil, fmt.Errorf("pdf: object %d: %w", num, err)
		}
		out.WriteString("\nendobj\n")
	}

	max := 0
	for _, num := range f.nums {
		if num > max {
			max = num
		}
	}
	xrefPos := out.Len()
	out.WriteString("xref\n")
	out.WriteString("0 " + strconv.Itoa(max+1) + "\n")
	out.WriteString("0000000000 65535 f \n")
	for num := 1; num <= max; num++ {
		if _, ok := f.objs[num]; !ok {
			out.WriteString("0000000000 65535 f \n")
			continue
		}
		out.WriteString(fmtOffset(offsets[num]))
		out.WriteString(" 00000 n \n")
	}

	trailer := f.Trailer.Clone()
	trailer.Delete("Prev")
	trailer.Delete("XRefStm")
	trailer.Set("Size", Int(max+1))
	if !trailer.Has("Root") {
		return nil, fmt.Errorf("pdf: trailer has no /Root")
	}
	if !trailer.Has("ID") {
		sum := md5.Sum(append(out.Bytes(), byte(max)))
		trailer.Set("ID", Array{String(sum[:]), String(sum[:])})
	}
	out.WriteString("trailer\n")
	if err := writeValue(&out, trailer); err != nil {
		return nil, fmt.Errorf("pdf: trailer: %w", err)
	}
	out.WriteString("\nstartxref\n" + strconv.Itoa(xrefPos) + "\n%%EOF\n")
	return out.Bytes(), nil
}

func fmtOffset(off int) string {
	s := strconv.Itoa(off)
	for len(s) < 10 {
		s = "0" + s
	}
	return s
}

// writeValue renders a value in PDF syntax.
func writeValue(out *bytes.Buffer, v Object) error {
	switch v := v.(type) {
	case nil:
		out.WriteString("null")
	case Null:
		out.WriteString("null")
	case Bool:
		if v {
			out.WriteString("true")
		} else {
			out.WriteString("false")
		}
	case Int:
		out.WriteString(strconv.FormatInt(int64(v), 10))
	case Real:
		out.WriteString(formatReal(float64(v)))
	case Name:
		out.WriteString(formatName(string(v)))
	case String:
		out.WriteString(formatString(string(v)))
	case Ref:
		out.WriteString(fmt.Sprintf("%d %d R", v.Num, v.Gen))
	case Array:
		out.WriteString("[")
		for i, e := range v {
			if i > 0 {
				out.WriteString(" ")
			}
			if err := writeValue(out, e); err != nil {
				return err
			}
		}
		out.WriteString("]")
	case *Dict:
		out.WriteString("<<")
		// Sorted keys keep the output reproducible: two runs over the same
		// document produce the same bytes, which makes diffs and tests useful.
		for _, k := range v.SortedKeys() {
			out.WriteString(" ")
			out.WriteString(formatName(k))
			out.WriteString(" ")
			if err := writeValue(out, v.Get(k)); err != nil {
				return err
			}
		}
		out.WriteString(" >>")
	case *Stream:
		if v.Dict == nil {
			return fmt.Errorf("stream without dictionary")
		}
		if err := writeValue(out, v.Dict); err != nil {
			return err
		}
		out.WriteString("\nstream\n")
		out.Write(v.Raw)
		out.WriteString("\nendstream")
	case int:
		out.WriteString(strconv.Itoa(v))
	case string:
		out.WriteString(formatString(v))
	default:
		return fmt.Errorf("%w: cannot write %T", ErrUnsupported, v)
	}
	return nil
}

// Compress flate-compresses a stream body, replacing any existing filter.
func (s *Stream) Compress(level int) error {
	var buf bytes.Buffer
	w, err := zlib.NewWriterLevel(&buf, level)
	if err != nil {
		return err
	}
	if _, err := w.Write(s.Raw); err != nil {
		return err
	}
	if err := w.Close(); err != nil {
		return err
	}
	if len(buf.Bytes()) >= len(s.Raw)+16 {
		// Incompressible; keep it stored.
		s.Dict.Delete("Filter")
		s.Dict.Delete("DecodeParms")
		return nil
	}
	s.Raw = buf.Bytes()
	s.Dict.Set("Filter", Name("FlateDecode"))
	s.Dict.Delete("DecodeParms")
	return nil
}

// Decompress returns a copy of the stream with filters removed.
func (f *File) Decompress(s *Stream) (*Stream, error) {
	body, err := f.decodeStream(s)
	if err != nil {
		return nil, err
	}
	d := s.Dict.Clone()
	d.Delete("Filter")
	d.Delete("DecodeParms")
	return &Stream{Dict: d, Raw: body}, nil
}

// SetText sets the dictionary key to a UTF-16BE string with a byte order
// mark, the form PDF uses for text that must survive non-ASCII characters.
func TextUTF16(s string) String {
	var b []byte
	b = append(b, 0xfe, 0xff)
	for _, r := range s {
		if r < 0x10000 {
			b = append(b, byte(r>>8), byte(r))
			continue
		}
		r -= 0x10000
		hi := 0xd800 + rune((r>>10)&0x3ff)
		lo := 0xdc00 + rune(r&0x3ff)
		b = append(b, byte(hi>>8), byte(hi), byte(lo>>8), byte(lo))
	}
	return String(b)
}
