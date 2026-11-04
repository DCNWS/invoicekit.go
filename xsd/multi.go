package xsd

import (
	"fmt"
	"os"
	"path"
)

// LoadMulti parses several schema documents into one model, which is how both
// UBL document types share a single component set.
func LoadMulti(dir string, files []string) (*Model, error) {
	r := &reader{
		m:      NewModel(),
		loaded: map[string]bool{},
		simple: map[string]bool{},
	}
	l := FileLoader(dir)
	for _, f := range files {
		p, src, err := readSchema(dir, f)
		if err != nil {
			return nil, err
		}
		// The path that was actually read becomes the base for that
		// document's own imports, so relative schemaLocation hrefs resolve
		// against the directory the set lives in rather than the caller's.
		if err := r.file(p, src, l, nil); err != nil {
			return nil, err
		}
	}
	if err := r.resolve(); err != nil {
		return nil, err
	}
	r.m.Source = fmt.Sprintf("%d schema files in %s, %d components", len(r.loaded), dir, len(r.m.Types)+len(r.m.Decls))
	return r.m, nil
}

// Dir returns the directory of a path, for provenance strings.
func Dir(p string) string { return path.Dir(p) }

// readSchema reads one top-level schema document. A relative name that does
// not exist where the command stands is looked for under dir, which is what
// makes "-dir DIR -schema NAME.xsd" work: the flag says where the set lives,
// and a caller should not have to cd into it as well.
func readSchema(dir, f string) (string, []byte, error) {
	if src, err := os.ReadFile(f); err == nil {
		return f, src, nil
	} else if dir == "" || dir == "." || path.IsAbs(f) {
		return "", nil, fmt.Errorf("read %s: %w", f, err)
	}
	alt := path.Join(dir, filepathClean(f))
	src, err := os.ReadFile(alt)
	if err != nil {
		return "", nil, err
	}
	return alt, src, nil
}
