package pdf

import "bytes"

// streamLength works out how many bytes a stream body holds. A declared
// /Length is trusted only when it leaves the body up against the endstream
// keyword; when it is missing, points at something that is not an integer, or
// is simply wrong (all of which happen in documents in circulation), the body
// is measured by scanning for the keyword instead.
func (f *File) streamLength(data []byte, d *Dict, bodyStart int) (int, bool) {
	if n, ok := f.declaredLength(d); ok && atEndStream(data, bodyStart+n) {
		return n, true
	}
	i := bytes.Index(data[bodyStart:], []byte("endstream"))
	if i < 0 {
		return 0, false
	}
	end := bodyStart + i
	// Trim the run of end-of-line markers in front of the keyword. Producers
	// write one; a binary body can happen to end in a newline.
	for end > bodyStart && (data[end-1] == '\n' || data[end-1] == '\r') {
		end--
	}
	return end - bodyStart, true
}

// declaredLength returns a stream dictionary's /Length as a byte count,
// following an indirect reference. The referenced integer often lives in an
// object stream and so may not have been parsed yet when the stream is read:
// it is fetched from the cross-reference table rather than assumed to be
// present. The in-progress guard stops a pathological /Length cycle.
func (f *File) declaredLength(d *Dict) (int, bool) {
	v := d.Get("Length")
	if rf, isRef := v.(Ref); isRef {
		if f.loadingLen[rf.Num] {
			return 0, false
		}
		if f.loadingLen == nil {
			f.loadingLen = map[int]bool{}
		}
		f.loadingLen[rf.Num] = true
		v = f.lengthObject(rf.Num)
		delete(f.loadingLen, rf.Num)
	}
	n, ok := v.(Int)
	if !ok || int(n) < 0 {
		return 0, false
	}
	return int(n), true
}

// lengthObject returns the object a /Length reference points at, loading it
// from its cross-reference entry if the general loader has not reached it yet.
// Anything unreadable counts as an absent length, which sends the caller down
// the scanning path.
func (f *File) lengthObject(num int) Object {
	if o := f.objs[num]; o != nil {
		return o
	}
	e, ok := f.table[num]
	if !ok || e.offset <= 0 || e.offset >= len(f.chars) {
		return Null{}
	}
	o, err := parseObjectAt(f.chars, e.offset, f)
	if err != nil {
		return Null{}
	}
	return o
}

// atEndStream reports whether the endstream keyword follows at pos, allowing
// for the whitespace one producer or another leaves in front of it.
func atEndStream(data []byte, pos int) bool {
	for pos < len(data) && (data[pos] == '\r' || data[pos] == '\n' || data[pos] == ' ' || data[pos] == '\t') {
		pos++
	}
	return bytes.HasPrefix(data[pos:], []byte("endstream"))
}
