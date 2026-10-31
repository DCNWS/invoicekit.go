package facturx

import (
	"strconv"
	"strings"
	"time"
	"unicode/utf16"
)

// decodeText turns the bytes of a PDF text string into Go text. PDF leaves the
// encoding to a byte order mark: UTF-16BE when it starts FEFF, UTF-16LE when it
// starts FFFE, UTF-8 when a BOM says so, and otherwise one byte per character in
// the document's default encoding, which in practice means Latin-1 for the
// characters above 0x7f.
func decodeText(s string) string {
	b := []byte(s)
	switch {
	case len(b) >= 2 && b[0] == 0xfe && b[1] == 0xff:
		return decodeUTF16(b[2:], false)
	case len(b) >= 2 && b[0] == 0xff && b[1] == 0xfe:
		return decodeUTF16(b[2:], true)
	case len(b) >= 3 && b[0] == 0xef && b[1] == 0xbb && b[2] == 0xbf:
		return string(b[3:])
	}
	var out strings.Builder
	for _, c := range b {
		if c < 0x80 {
			out.WriteByte(c)
			continue
		}
		out.WriteRune(rune(cp1252(c)))
	}
	return out.String()
}

// cp1252 maps the Windows-1252 upper half, which is what most PDF producers
// actually use, to Unicode. The 0x80-0x9f range is the one people notice:
// curly quotes and dashes come out of word processors in those codes.
// cp1252Upper holds the 32 codes Windows-1252 puts where Latin-1 has control
// characters. It is a rune table on purpose: indexing a string yields a byte,
// which turns every multi-byte character in it into mojibake.
var cp1252Upper = [32]rune{
	0x20ac, 0x0081, 0x201a, 0x0192, 0x201e, 0x2026, 0x2020, 0x2021,
	0x02c6, 0x2030, 0x0160, 0x2039, 0x0152, 0x008d, 0x017d, 0x008f,
	0x0090, 0x2018, 0x2019, 0x201c, 0x201d, 0x2022, 0x2013, 0x2014,
	0x02dc, 0x2122, 0x0161, 0x203a, 0x0153, 0x009d, 0x017e, 0x0178,
}

func cp1252(c byte) rune {
	if c < 0x80 {
		return rune(c)
	}
	if c >= 0xa0 {
		return rune(c) // identical to Latin-1 from 0xa0 up
	}
	return cp1252Upper[c-0x80]
}

func decodeUTF16(b []byte, little bool) string {
	units := make([]uint16, 0, len(b)/2)
	for i := 0; i+1 < len(b); i += 2 {
		if little {
			units = append(units, uint16(b[i])|uint16(b[i+1])<<8)
		} else {
			units = append(units, uint16(b[i])<<8|uint16(b[i+1]))
		}
	}
	return string(utf16.Decode(units))
}

// pdfTime parses the PDF date format: D:YYYYMMDDHHmmSS with an optional
// fraction and time zone, e.g. "D:20260214090000+01'00'". Missing parts are
// taken as zero, which is what ISO 32000 says readers should do.
func pdfTime(s string) time.Time {
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(s, "D:")
	if len(s) < 8 {
		return time.Time{}
	}
	num := func(at, n int) int {
		v, err := strconv.Atoi(s[at : at+n])
		if err != nil {
			return 0
		}
		return v
	}
	year, month, day := num(0, 4), num(4, 2), num(6, 2)
	hour, min, sec := 0, 0, 0
	if len(s) >= 14 {
		hour, min, sec = num(8, 2), num(10, 2), num(12, 2)
	}
	loc := time.UTC
	if len(s) > 14 {
		rest := s[14:]
		if i := strings.IndexAny(rest, "+-Zz"); i >= 0 {
			off := strings.ReplaceAll(rest[i:], "'", "")
			if off[0] == 'Z' || off[0] == 'z' {
				loc = time.UTC
			} else if len(off) >= 5 {
				sign := 1
				if off[0] == '-' {
					sign = -1
				}
				oh, _ := strconv.Atoi(off[1:3])
				om := 0
				if len(off) >= 5 {
					om, _ = strconv.Atoi(off[3:5])
				}
				loc = time.FixedZone("", sign*(oh*3600+om*60))
			}
		}
	}
	t := time.Date(year, time.Month(month), day, hour, min, sec, 0, loc)
	if year == 0 || month == 0 || day == 0 {
		return time.Time{}
	}
	return t
}
