package pdf

import (
	"bytes"
	"fmt"
)

// File is a parsed PDF document.
type File struct {
	// Version is the header version, e.g. "1.7".
	Version string

	// Trailer is the document trailer dictionary. /Prev and /XRefStm are
	// dropped on output because the cross-reference table is rebuilt.
	Trailer *Dict

	objs       map[int]Object
	nums       []int // numbers of loaded objects, ascending
	max        int
	chars      []byte // original bytes, kept so streams can be copied verbatim
	table      map[int]xrefEntry
	loadingLen map[int]bool // /Length objects being resolved, to stop cycles
}

// Object returns the object with the given number, or nil when absent.
func (f *File) Object(num int) Object { return f.objs[num] }

// Add stores a new object and returns its number.
func (f *File) Add(obj Object) int {
	f.max++
	f.objs[f.max] = obj
	f.reindex()
	return f.max
}

// Set replaces the object stored under num.
func (f *File) Set(num int, obj Object) {
	f.objs[num] = obj
	if num > f.max {
		f.max = num
	}
	f.reindex()
}

// Delete frees the object number.
func (f *File) Delete(num int) {
	delete(f.objs, num)
	f.reindex()
}

func (f *File) reindex() {
	f.nums = f.nums[:0]
	highest := 0
	for n := range f.objs {
		f.nums = append(f.nums, n)
		if n > highest {
			highest = n
		}
	}
	if highest > f.max {
		f.max = highest
	}
	// Insertion sort is plenty for the handful of hundred objects an
	// invoice carries and avoids pulling in the sort package here.
	for i := 1; i < len(f.nums); i++ {
		for j := i; j > 0 && f.nums[j-1] > f.nums[j]; j-- {
			f.nums[j-1], f.nums[j] = f.nums[j], f.nums[j-1]
		}
	}
}

// Resolve replaces a Ref by the object it points at, recursively.
func (f *File) Resolve(obj Object) Object {
	for {
		r, ok := obj.(Ref)
		if !ok {
			return obj
		}
		obj = f.objs[r.Num]
		if obj == nil {
			return Null{}
		}
	}
}

// Root returns the catalog dictionary.
func (f *File) Root() (*Dict, error) {
	d := f.Trailer.DictValue("Root", f)
	if d == nil || d.TypeName() != "Catalog" {
		return nil, fmt.Errorf("pdf: document has no catalog")
	}
	return d, nil
}

// Parse reads a PDF document.
func Parse(data []byte) (*File, error) {
	f := &File{objs: map[int]Object{}, chars: data}
	if len(data) < 8 || !bytes.HasPrefix(data, []byte("%PDF-")) {
		return nil, fmt.Errorf("pdf: missing %%PDF- header")
	}
	end := bytes.IndexByte(data[5:], '\n')
	if end < 0 {
		return nil, fmt.Errorf("pdf: malformed header")
	}
	f.Version = string(data[5 : 5+end])
	if c := f.Version[len(f.Version)-1]; c == '\r' {
		f.Version = f.Version[:len(f.Version)-1]
	}

	start, err := findStartXref(data)
	if err != nil {
		return nil, err
	}
	if err := f.loadXrefs(data, start); err != nil {
		return nil, err
	}
	if f.Trailer == nil {
		return nil, fmt.Errorf("pdf: no cross-reference data found")
	}
	if f.Trailer.Has("Encrypt") {
		return nil, fmt.Errorf("%w: encrypted documents", ErrUnsupported)
	}
	if err := f.loadObjects(data); err != nil {
		return nil, err
	}
	f.reindex()
	return f, nil
}

// findStartXref locates the startxref keyword nearest the end of file and
// returns the offset it names.
func findStartXref(data []byte) (int, error) {
	tail := len(data) - 1024
	if tail < 0 {
		tail = 0
	}
	i := bytes.LastIndex(data[tail:], []byte("startxref"))
	if i < 0 {
		return 0, fmt.Errorf("pdf: no startxref keyword")
	}
	r := &scanner{data: data, pos: tail + i + len("startxref")}
	v, err := r.number()
	if err != nil {
		return 0, fmt.Errorf("pdf: startxref offset: %w", err)
	}
	switch n := v.(type) {
	case Int:
		if int64(n) < 0 || int(n) >= len(data) {
			return 0, fmt.Errorf("pdf: startxref offset %d out of range", n)
		}
		return int(n), nil
	}
	return 0, fmt.Errorf("pdf: startxref offset is not an integer")
}

// xrefEntry records where an object lives.
type xrefEntry struct {
	offset int // direct offset, for classic entries
	stm    int // object number of the object stream, for stream entries
	idx    int // index inside that object stream
	gen    int
	free   bool
	seen   bool
}

// loadXrefs walks the cross-reference chain, newest first. Entries found
// later (older sections) never overwrite newer ones.
func (f *File) loadXrefs(data []byte, pos int) error {
	f.table = map[int]xrefEntry{}
	visited := map[int]bool{}
	for pos != 0 && !visited[pos] {
		visited[pos] = true
		next, err := f.readXrefSection(data, pos)
		if err != nil {
			return err
		}
		if next < 0 {
			break
		}
		pos = next
	}
	return nil
}

func (f *File) readXrefSection(data []byte, pos int) (int, error) {
	if pos >= len(data) {
		return -1, fmt.Errorf("pdf: cross-reference offset %d out of range", pos)
	}
	r := &scanner{data: data, pos: pos}
	if r.skipToToken(); bytes.HasPrefix(data[r.pos:], []byte("xref")) {
		return f.readClassicXref(data, r)
	}
	// A cross-reference section written as an indirect object holding an
	// XRef stream.
	num, gen, err := r.objectHeader()
	if err != nil {
		return -1, fmt.Errorf("pdf: cross-reference section at %d: %w", pos, err)
	}
	s, err := f.readStream(data, r, nil, num, gen)
	if err != nil {
		return -1, err
	}
	if s.Dict.NameValue("Type") != "XRef" {
		return -1, fmt.Errorf("%w: indirect object %d is not an XRef stream", ErrUnsupported, num)
	}
	if f.Trailer == nil {
		f.Trailer = s.Dict
	}
	if err := f.readXrefStreamEntries(s); err != nil {
		return -1, err
	}
	if prev, ok := s.Dict.IntValue("Prev"); ok {
		return int(prev), nil
	}
	return -1, nil
}

func (f *File) readClassicXref(data []byte, r *scanner) (int, error) {
	r.pos += len("xref")
	for {
		r.skipToToken()
		if bytes.HasPrefix(data[r.pos:], []byte("trailer")) {
			r.pos += len("trailer")
			v, err := r.value(f)
			if err != nil {
				return -1, err
			}
			tr, ok := v.(*Dict)
			if !ok {
				return -1, fmt.Errorf("pdf: trailer is not a dictionary")
			}
			if f.Trailer == nil {
				f.Trailer = tr
			}
			if prev, ok := tr.IntValue("Prev"); ok {
				return int(prev), nil
			}
			return -1, nil
		}
		startObj64, err := r.intNumber()
		if err != nil {
			return -1, fmt.Errorf("pdf: cross-reference subsection: %w", err)
		}
		count64, err := r.intNumber()
		if err != nil {
			return -1, fmt.Errorf("pdf: cross-reference subsection size: %w", err)
		}
		if startObj64 < 0 || count64 < 0 || startObj64+count64 > 1<<30 {
			return -1, fmt.Errorf("pdf: cross-reference subsection %d %d out of range", startObj64, count64)
		}
		startObj, count := int(startObj64), int(count64)
		for i := 0; i < count; i++ {
			// Entries are fixed-width in a conforming file, but tolerate
			// anything whitespace-separated.
			e, err := r.xrefEntryText()
			if err != nil {
				return -1, err
			}
			num := startObj + i
			if e.kind == 'n' {
				f.setEntry(num, xrefEntry{offset: e.offset, gen: e.gen, seen: true})
			} else {
				f.setEntry(num, xrefEntry{free: true, gen: e.gen, seen: true})
			}
		}
	}
}

type entryText struct {
	kind   byte
	offset int
	gen    int
}

// xrefEntryText reads one 20-byte cross-reference entry, accepting the
// common malformations (short leading zeros, single-space separators).
func (r *scanner) xrefEntryText() (entryText, error) {
	r.skipToToken()
	start := r.pos
	a, err := r.intNumber()
	if err != nil {
		return entryText{}, err
	}
	b, err := r.intNumber()
	if err != nil {
		return entryText{}, err
	}
	r.skipToToken()
	if r.pos >= len(r.data) {
		return entryText{}, fmt.Errorf("pdf: truncated cross-reference entry at %d", start)
	}
	kind := r.data[r.pos]
	if kind != 'n' && kind != 'f' && kind != 'o' {
		return entryText{}, fmt.Errorf("pdf: bad cross-reference entry %q", r.data[start:r.pos+1])
	}
	r.pos++
	k := byte('f')
	if kind == 'n' || kind == 'o' {
		k = 'n'
	}
	return entryText{kind: k, offset: int(a), gen: int(b)}, nil
}

// setEntry records an entry unless a newer section already has one.
func (f *File) setEntry(num int, e xrefEntry) {
	if old, ok := f.table[num]; ok && old.seen {
		return
	}
	f.table[num] = e
}

// readXrefStreamEntries decodes the W/Index encoded array of a cross-reference
// stream.
func (f *File) readXrefStreamEntries(s *Stream) error {
	d := s.Dict
	w, ok := intArray(d.Get("W"))
	if !ok || len(w) != 3 {
		return fmt.Errorf("pdf: XRef stream /W must be three integers")
	}
	idx := []int{0, int(orInt(d.Get("Size")))}
	if a, ok := intArray(d.Get("Index")); ok && len(a) >= 2 {
		idx = nil
		for i := 0; i+1 < len(a); i += 2 {
			idx = append(idx, a[i], a[i+1])
		}
	}
	raw, err := f.decodeStream(s)
	if err != nil {
		return err
	}
	row := w[0] + w[1] + w[2]
	if row <= 0 {
		return fmt.Errorf("pdf: XRef stream /W sums to zero")
	}
	pos := 0
	for k := 0; k+1 < len(idx); k += 2 {
		first, count := idx[k], idx[k+1]
		for i := 0; i < count; i++ {
			if pos+row > len(raw) {
				return fmt.Errorf("pdf: XRef stream truncated")
			}
			var typ int64
			if w[0] > 0 {
				typ = readBE(raw[pos : pos+w[0]])
				pos += w[0]
			} else {
				typ = 1
			}
			f2 := readBE(raw[pos : pos+w[1]])
			pos += w[1]
			f3 := readBE(raw[pos : pos+w[2]])
			pos += w[2]
			num := first + i
			if num < 0 {
				continue
			}
			switch typ {
			case 1:
				f.setEntry(num, xrefEntry{offset: int(f2), gen: int(f3), seen: true})
			case 2:
				f.setEntry(num, xrefEntry{stm: int(f2), idx: int(f3), seen: true})
			default:
				f.setEntry(num, xrefEntry{free: true, seen: true})
			}
		}
	}
	return nil
}

// loadObjects resolves every entry in the cross-reference table.
func (f *File) loadObjects(data []byte) error {
	// Direct objects first: object streams may be referenced by the
	// compressed entries but can never live inside one.
	type pending struct {
		num int
		e   xrefEntry
	}
	var compressed []pending
	for num, e := range f.table {
		if e.free {
			continue
		}
		if e.offset > 0 {
			obj, err := parseObjectAt(data, e.offset, f)
			if err != nil {
				return fmt.Errorf("pdf: object %d: %w", num, err)
			}
			f.objs[num] = obj
			continue
		}
		compressed = append(compressed, pending{num, e})
	}
	for _, p := range compressed {
		stmObj, err := f.objectFor(p.e.stm)
		if err != nil {
			return err
		}
		s, ok := stmObj.(*Stream)
		if !ok {
			return fmt.Errorf("%w: object %d holding object %d is not a stream", ErrUnsupported, p.e.stm, p.num)
		}
		if s.Dict.TypeName() != "ObjStm" {
			return fmt.Errorf("%w: object %d is %q, not an ObjStm", ErrUnsupported, p.e.stm, s.Dict.TypeName())
		}
		body, err := f.decodeStream(s)
		if err != nil {
			return err
		}
		n, _ := s.Dict.IntValue("N")
		first, _ := s.Dict.IntValue("First")
		if n <= 0 || first < 0 || first > int64(len(body)) {
			return fmt.Errorf("pdf: malformed object stream %d", p.e.stm)
		}
		pairs := make([]int64, 0, 2*n)
		r := &scanner{data: body}
		for i := int64(0); i < n; i++ {
			a, err := r.intNumber()
			if err != nil {
				return fmt.Errorf("pdf: object stream %d header: %w", p.e.stm, err)
			}
			b, err := r.intNumber()
			if err != nil {
				return fmt.Errorf("pdf: object stream %d header: %w", p.e.stm, err)
			}
			pairs = append(pairs, a, b)
		}
		for i := 0; i+1 < len(pairs); i += 2 {
			num := int(pairs[i])
			off := first + pairs[i+1]
			if off < 0 || off > int64(len(body)) {
				return fmt.Errorf("pdf: object %d offset out of range in object stream %d", num, p.e.stm)
			}
			obj, err := parseValueAt(body, int(off), f)
			if err != nil {
				return fmt.Errorf("pdf: object %d in stream %d: %w", num, p.e.stm, err)
			}
			f.objs[num] = obj
		}
	}
	return nil
}

// objectFor returns an object from the table, loading it if needed.
func (f *File) objectFor(num int) (Object, error) {
	if o := f.objs[num]; o != nil {
		return o, nil
	}
	e, ok := f.table[num]
	if !ok || e.offset <= 0 {
		return nil, fmt.Errorf("pdf: object %d not found", num)
	}
	obj, err := parseObjectAt(f.chars, e.offset, f)
	if err != nil {
		return nil, err
	}
	f.objs[num] = obj
	return obj, nil
}

// parseValueAt reads a bare object value at pos, as stored inside an object
// stream: no "num gen obj" header, and streams never appear there.
func parseValueAt(data []byte, pos int, f *File) (Object, error) {
	if pos < 0 || pos > len(data) {
		return nil, fmt.Errorf("pdf: object offset %d out of range", pos)
	}
	r := &scanner{data: data, pos: pos}
	return r.value(f)
}

// parseObjectAt reads "num gen obj value" at the given offset and returns the
// value. Streams keep their encoded bytes.
func parseObjectAt(data []byte, pos int, f *File) (Object, error) {
	r := &scanner{data: data, pos: pos}
	num, gen, err := r.objectHeader()
	if err != nil {
		return nil, err
	}
	_ = num
	_ = gen
	v, err := r.value(f)
	if err != nil {
		return nil, err
	}
	// A dictionary may be followed by a stream.
	if d, ok := v.(*Dict); ok {
		save := r.pos
		r.skipToToken()
		if bytes.HasPrefix(data[r.pos:], []byte("stream")) {
			return f.readStream(data, r, d, num, gen)
		}
		r.pos = save
	}
	return v, nil
}

// readStream finishes parsing a stream that follows a dictionary already
// consumed by r.
func (f *File) readStream(data []byte, r *scanner, d *Dict, num, gen int) (*Stream, error) {
	if d == nil {
		v, err := r.value(f)
		if err != nil {
			return nil, err
		}
		var ok bool
		if d, ok = v.(*Dict); !ok {
			return nil, fmt.Errorf("pdf: stream without dictionary (object %d)", num)
		}
	}
	r.skipToToken()
	if !bytes.HasPrefix(data[r.pos:], []byte("stream")) {
		return nil, fmt.Errorf("pdf: expected stream keyword in object %d", num)
	}
	r.pos += len("stream")
	// Skip exactly one end-of-line marker after the keyword.
	switch {
	case r.pos < len(data) && data[r.pos] == '\r':
		r.pos++
		if r.pos < len(data) && data[r.pos] == '\n' {
			r.pos++
		}
	case r.pos < len(data) && data[r.pos] == '\n':
		r.pos++
	}
	bodyStart := r.pos
	length, ok := f.streamLength(data, d, bodyStart)
	if !ok {
		return nil, fmt.Errorf("pdf: stream object %d has no usable /Length", num)
	}
	if length < 0 || r.pos+length > len(data) {
		return nil, fmt.Errorf("pdf: stream object %d length %d out of range", num, length)
	}
	raw := data[r.pos : r.pos+length]
	r.pos += length
	r.skipToToken()
	if !bytes.HasPrefix(data[r.pos:], []byte("endstream")) {
		return nil, fmt.Errorf("pdf: expected endstream in object %d", num)
	}
	r.pos += len("endstream")
	return &Stream{Dict: d, Raw: raw}, nil
}

// intArray converts an array of integers.
func intArray(v Object) ([]int, bool) {
	a, ok := v.(Array)
	if !ok {
		return nil, false
	}
	out := make([]int, 0, len(a))
	for _, e := range a {
		switch n := e.(type) {
		case Int:
			out = append(out, int(n))
		case Real:
			out = append(out, int(n))
		default:
			return nil, false
		}
	}
	return out, true
}

// orInt returns the integer value of a PDF number, or 0.
func orInt(v Object) int64 {
	switch n := v.(type) {
	case Int:
		return int64(n)
	case Real:
		return int64(n)
	}
	return 0
}

// readBE reads a big-endian unsigned integer of 1..8 bytes.
func readBE(b []byte) int64 {
	var v int64
	for _, c := range b {
		v = v<<8 | int64(c)
	}
	return v
}

// New returns an empty document: a catalogue in a plain trailer, ready for
// objects to be added. It is the counterpart of Parse, for producing a PDF from
// nothing rather than editing one.
func New(version string) *File {
	if version == "" {
		version = "1.7"
	}
	f := &File{objs: map[int]Object{}, Version: version, table: map[int]xrefEntry{}}
	f.Trailer = NewDict()
	catalog := NewDict()
	catalog.Set("Type", Name("Catalog"))
	f.Trailer.Set("Root", Ref{Num: f.Add(catalog)})
	f.reindex()
	return f
}
