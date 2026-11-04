package xsd

import (
	"bytes"
	"encoding/xml"
	"fmt"
	"io"
	"maps"
	"os"
	"path"
	"sort"
	"strconv"
	"strings"
)

// xsNS is the XML Schema namespace.
const xsNS = "http://www.w3.org/2001/XMLSchema"

func xs(local string) string { return "{" + xsNS + "}" + local }

// Loader returns the schema document referenced by href from the directory dir.
type Loader func(dir, href string) ([]byte, error)

// FileLoader reads imports and includes from a directory tree, falling back to
// a search by base name in dir. The vendored UBL tree is flattened, so the
// ../common/... paths in the maindoc schemas do not resolve as written.
func FileLoader(dir string) Loader {
	return func(d, href string) ([]byte, error) {
		base := d
		if base == "" || base == "." {
			base = dir
		}
		p := path.Join(base, filepathClean(href))
		if b, err := os.ReadFile(p); err == nil {
			return b, nil
		}
		return os.ReadFile(path.Join(dir, path.Base(href)))
	}
}

// Load parses one schema document and everything it imports or includes.
func Load(p string, src []byte, l Loader) (*Model, error) {
	r := &reader{
		m:      NewModel(),
		loaded: map[string]bool{},
		simple: map[string]bool{},
	}
	if err := r.file(p, src, l, nil); err != nil {
		return nil, err
	}
	if err := r.resolve(); err != nil {
		return nil, err
	}
	// Compile the matchers now: validation only ever reads them, which is what
	// makes one cached model safe to use from several goroutines.
	r.m.Compile()
	return r.m, nil
}

type reader struct {
	m      *Model
	loaded map[string]bool
	simple map[string]bool // simpleType declarations, by Clark name
}

// xnode is a minimal XML tree that keeps namespace declarations, which
// encoding/xml resolves away and xs:import does not care about.
type xnode struct {
	name  string
	attrs map[string]string
	xmlns map[string]string
	kids  []*xnode
}

func (n *xnode) at(local string) *xnode {
	for _, k := range n.kids {
		if k.name == xs(local) {
			return k
		}
	}
	return nil
}

func (r *reader) file(p string, src []byte, l Loader, outer map[string]string) error {
	key := filepathClean(p)
	if r.loaded[key] {
		return nil
	}
	r.loaded[key] = true
	root, err := parseTree(src)
	if err != nil {
		return fmt.Errorf("%s: %w", key, err)
	}
	scope := make(map[string]string, len(outer)+len(root.xmlns))
	maps.Copy(scope, outer)
	maps.Copy(scope, root.xmlns)
	switch root.name {
	case xs("schemas"):
		for _, k := range root.kids {
			if k.name == xs("schema") {
				if err := r.schema(k, path.Dir(key), l, scope); err != nil {
					return err
				}
			}
		}
		return nil
	case xs("schema"):
		return r.schema(root, path.Dir(key), l, scope)
	}
	return fmt.Errorf("%s: root element is %s, want xs:schema", key, root.name)
}

func (r *reader) schema(n *xnode, dir string, l Loader, scope map[string]string) error {
	tns := n.attrs["targetNamespace"]
	efd := n.attrs["elementFormDefault"]
	for _, k := range n.kids {
		switch k.name {
		case xs("import"), xs("include"):
			href := k.attrs["schemaLocation"]
			if href == "" {
				continue
			}
			if l == nil {
				return fmt.Errorf("no loader for schemaLocation %q (in %s)", href, dir)
			}
			src, err := l(dir, href)
			if err != nil {
				return fmt.Errorf("%s: %s: %w", dir, href, err)
			}
			if err := r.file(path.Join(dir, filepathClean(href)), src, l, scope); err != nil {
				return err
			}
		case xs("element"):
			d, err := r.decl(k, tns, efd, scope)
			if err != nil {
				return err
			}
			if d != nil {
				if old, ok := r.m.Decls[d.ID]; ok && old != d {
					return fmt.Errorf("duplicate global element %s", d.ID)
				}
				if _, ok := r.m.Decls[d.ID]; !ok {
					r.m.Roots = append(r.m.Roots, d.ID)
				}
				r.m.Decls[d.ID] = d
			}
		case xs("complexType"):
			if k.attrs["name"] == "" {
				continue
			}
			t, err := r.complex(k, tns, efd, scope)
			if err != nil {
				return err
			}
			if _, ok := r.m.Types[t.ID]; ok {
				return fmt.Errorf("duplicate complexType %s", t.ID)
			}
			r.m.Types[t.ID] = t
		case xs("simpleType"):
			if nm := k.attrs["name"]; nm != "" {
				r.simple[clark(tns, nm)] = true
			}
		case xs("group"), xs("attributeGroup"):
			return fmt.Errorf("xs:%s is not supported (%s)", local(k.name), k.attrs["name"])
		}
	}
	return nil
}

func (r *reader) decl(n *xnode, tns, efd string, scope map[string]string) (*Decl, error) {
	nm := n.attrs["name"]
	if nm == "" {
		return nil, nil // a global xs:element with ref: nothing to declare
	}
	d := &Decl{ID: clark(tns, nm), NS: tns}
	if ty := n.attrs["type"]; ty != "" {
		q, alt, err := qnameAlt(ty, tns, scope)
		if err != nil {
			return nil, err
		}
		d.TypeRef, d.TypeAlt = q, alt
		return d, nil
	}
	if ct := n.at("complexType"); ct != nil {
		t, err := r.complex(ct, tns, efd, scope)
		if err != nil {
			return nil, err
		}
		d.Type = t
	}
	return d, nil
}

func (r *reader) complex(n *xnode, tns, efd string, scope map[string]string) (*Type, error) {
	t := &Type{NS: tns}
	if nm := n.attrs["name"]; nm != "" {
		t.ID = clark(tns, nm)
	}
	for _, k := range n.kids {
		switch k.name {
		case xs("sequence"):
			tt, err := r.container(k, Seq, tns, efd, scope)
			if err != nil {
				return nil, err
			}
			t.Term = tt
		case xs("choice"):
			tt, err := r.container(k, Choice, tns, efd, scope)
			if err != nil {
				return nil, err
			}
			t.Term = tt
		case xs("all"):
			return nil, fmt.Errorf("xs:all is not supported (in %s)", t.ID)
		case xs("simpleContent"):
			t.Term = nil // a text value with attributes: a leaf
		case xs("complexContent"):
			tt, err := r.complexContent(k, tns, efd, scope)
			if err != nil {
				return nil, err
			}
			t.Term = tt
		}
	}
	return t, nil
}

// complexContent handles xs:complexContent/xs:extension and xs:restriction,
// where the content model is the base type's content plus the derived part.
func (r *reader) complexContent(n *xnode, tns, efd string, scope map[string]string) (*Term, error) {
	var own *Term
	var base, baseAlt string
	for _, k := range n.kids {
		if k.name != xs("extension") && k.name != xs("restriction") {
			continue
		}
		if b := k.attrs["base"]; b != "" {
			q, alt, err := qnameAlt(b, tns, scope)
			if err != nil {
				return nil, err
			}
			base, baseAlt = q, alt
		}
		for _, g := range k.kids {
			var (
				tt  *Term
				err error
			)
			switch g.name {
			case xs("sequence"):
				tt, err = r.container(g, Seq, tns, efd, scope)
			case xs("choice"):
				tt, err = r.container(g, Choice, tns, efd, scope)
			default:
				continue
			}
			if err != nil {
				return nil, err
			}
			own = tt
		}
	}
	if base == "" || base == clark(xsNS, "anyType") || base == clark(xsNS, "anySimpleType") {
		return own, nil
	}
	ref := &Term{Kind: Ref, Min: 1, Max: 1, TypeRef: base, TypeAlt: baseAlt}
	switch {
	case own == nil:
		return ref, nil
	case ref == nil:
		return own, nil
	}
	seq := &Term{Kind: Seq, Min: 1, Max: 1, Parts: []*Term{ref, own}}
	return seq, nil
}

func (r *reader) container(n *xnode, kind Kind, tns, efd string, scope map[string]string) (*Term, error) {
	t := &Term{Kind: kind}
	var err error
	if t.Min, t.Max, err = occ(n); err != nil {
		return nil, err
	}
	for _, k := range n.kids {
		var p *Term
		var perr error
		switch k.name {
		case xs("element"):
			p, perr = r.element(k, tns, efd, scope)
		case xs("sequence"):
			p, perr = r.container(k, Seq, tns, efd, scope)
		case xs("choice"):
			p, perr = r.container(k, Choice, tns, efd, scope)
		case xs("any"):
			p, perr = r.anyTerm(k)
		case xs("all"):
			return nil, fmt.Errorf("xs:all is not supported")
		case xs("group"):
			return nil, fmt.Errorf("xs:group is not supported (%s)", k.attrs["ref"])
		default:
			continue
		}
		if perr != nil {
			return nil, perr
		}
		if p != nil {
			t.Parts = append(t.Parts, p)
		}
	}
	return t, nil
}

// anyTerm reads an xs:any wildcard. Its occurrence attributes are its own, so
// a bare xs:any accepts exactly one element of any name: that is what UBL's
// ExtensionContentType declares, and reading the counts as zero would make the
// position reject every extension content element.
func (r *reader) anyTerm(n *xnode) (*Term, error) {
	min, max, err := occ(n)
	if err != nil {
		return nil, err
	}
	return &Term{Kind: Any, Min: min, Max: max, AnyNS: n.attrs["namespace"]}, nil
}

func (r *reader) element(n *xnode, tns, efd string, scope map[string]string) (*Term, error) {
	t := &Term{Kind: Elem}
	var err error
	if t.Min, t.Max, err = occ(n); err != nil {
		return nil, err
	}
	if ref := n.attrs["ref"]; ref != "" {
		q, err := qname(ref, tns, scope)
		if err != nil {
			return nil, err
		}
		t.DeclRef = q
		t.Name = q
		return t, nil
	}
	nm := n.attrs["name"]
	if nm == "" {
		return nil, fmt.Errorf("xs:element has neither name nor ref")
	}
	ns := tns
	switch n.attrs["form"] {
	case "unqualified":
		ns = ""
	case "qualified":
		ns = tns
	default:
		if efd == "unqualified" {
			ns = ""
		}
	}
	t.Name = clark(ns, nm)
	if ty := n.attrs["type"]; ty != "" {
		q, alt, err := qnameAlt(ty, tns, scope)
		if err != nil {
			return nil, err
		}
		t.TypeRef, t.TypeAlt = q, alt
		return t, nil
	}
	if ct := n.at("complexType"); ct != nil {
		c, err := r.complex(ct, tns, efd, scope)
		if err != nil {
			return nil, err
		}
		t.Type = c
	}
	return t, nil
}

// resolve wires DeclRef and TypeRef to components and reports any reference
// that no loaded schema defines. A reference to a simpleType is not a miss: it
// means the element is a leaf.
func (r *reader) resolve() error {
	for _, d := range r.m.Decls {
		if d.TypeRef == "" {
			continue
		}
		if t, ok := r.m.Types[d.TypeRef]; ok {
			d.Type = t
			continue
		}
		if ref, ty, ok := r.lookup(d.TypeRef, d.TypeAlt); ok {
			d.TypeRef, d.Type = ref, ty
			continue
		}
		return fmt.Errorf("global element %s: type %s is not defined", d.ID, d.TypeRef)
	}
	var walk func(t *Term, seen map[*Term]bool) error
	walk = func(t *Term, seen map[*Term]bool) error {
		if t == nil || seen[t] {
			return nil
		}
		seen[t] = true
		switch {
		case t.DeclRef != "":
			d, ok := r.m.Decls[t.DeclRef]
			if !ok {
				return fmt.Errorf("element %s is not declared", t.DeclRef)
			}
			t.Decl = d
			t.Type = d.Type
		case t.TypeRef != "":
			if ty, ok := r.m.Types[t.TypeRef]; ok {
				t.Type = ty
			} else if ref, ty, ok := r.lookup(t.TypeRef, t.TypeAlt); ok {
				t.TypeRef, t.Type = ref, ty
			} else {
				return fmt.Errorf("complexType %s is not defined (for element %s)", t.TypeRef, t.Name)
			}
		}
		if err := walk(t.Type.term(), seen); err != nil {
			return err
		}
		for _, p := range t.Parts {
			if err := walk(p, seen); err != nil {
				return err
			}
		}
		return nil
	}
	r.substRefs()
	seen := map[*Term]bool{}
	for _, t := range r.m.Types {
		if err := walk(t.Term, seen); err != nil {
			return err
		}
	}
	for _, d := range r.m.Decls {
		if err := walk(d.Type.term(), seen); err != nil {
			return err
		}
	}
	return nil
}

// substRefs replaces complexContent base placeholders with the base type's own
// content, so a derived type is matched as base content plus its extension.
func (r *reader) substRefs() {
	var walk func(t *Term, seen map[*Term]bool)
	walk = func(t *Term, seen map[*Term]bool) {
		if t == nil || seen[t] {
			return
		}
		seen[t] = true
		if t.Kind != Ref && len(t.Parts) == 0 {
			return
		}
		if len(t.Parts) > 0 {
			parts := make([]*Term, 0, len(t.Parts))
			for _, p := range t.Parts {
				if p.Kind == Ref {
					if b := r.m.Types[p.TypeRef]; b != nil && b.Term != nil {
						parts = append(parts, b.Term)
					}
					continue
				}
				parts = append(parts, p)
			}
			t.Parts = parts
		}
		for _, p := range t.Parts {
			walk(p, seen)
		}
	}
	for _, ty := range r.m.Types {
		if ty.Term != nil && ty.Term.Kind == Ref {
			if b := r.m.Types[ty.Term.TypeRef]; b != nil {
				ty.Term = b.Term
			} else {
				ty.Term = nil
			}
		}
		walk(ty.Term, map[*Term]bool{})
	}
	for _, d := range r.m.Decls {
		if d.Type != nil && d.Type.Term != nil && d.Type.Term.Kind == Ref {
			if b := r.m.Types[d.Type.Term.TypeRef]; b != nil {
				d.Type.Term = b.Term
			} else {
				d.Type.Term = nil
			}
		}
	}
}

// term returns the content term of a type, tolerating a nil type.
func (t *Type) term() *Term {
	if t == nil {
		return nil
	}
	return t.Term
}

// lookup resolves a type reference against components, simple types and the
// built-in types, trying the fallback name when the primary one misses.
func (r *reader) lookup(ref, alt string) (string, *Type, bool) {
	for _, c := range []string{ref, alt} {
		if c == "" {
			continue
		}
		if ty, ok := r.m.Types[c]; ok {
			return c, ty, true
		}
		if r.simple[c] || knownBuiltin(c) {
			return c, nil, true // a leaf: text value, no element children
		}
	}
	return "", nil, false
}

// knownBuiltin accepts XSD built-in types used as element types or bases.
func knownBuiltin(id string) bool {
	ns, local := splitClark(id)
	return ns == xsNS && builtinTypes[local]
}

var builtinTypes = map[string]bool{
	"anyType": true, "anySimpleType": true, "string": true, "token": true,
	"normalizedString": true, "decimal": true, "integer": true, "nonNegativeInteger": true,
	"positiveInteger": true, "boolean": true, "date": true, "time": true,
	"dateTime": true, "duration": true, "base64Binary": true, "hexBinary": true,
	"QName": true, "NCName": true, "ID": true, "IDREF": true, "anyURI": true,
}

// occ reads minOccurs/maxOccurs with their defaults.
func occ(n *xnode) (min, max int, err error) {
	min, max = 1, 1
	if s := n.attrs["minOccurs"]; s != "" {
		if min, err = strconv.Atoi(s); err != nil {
			return 0, 0, fmt.Errorf("minOccurs %q: %w", s, err)
		}
	}
	if s := n.attrs["maxOccurs"]; s != "" {
		if s == "unbounded" {
			max = -1
		} else if max, err = strconv.Atoi(s); err != nil {
			return 0, 0, fmt.Errorf("maxOccurs %q: %w", s, err)
		}
	}
	return min, max, nil
}

// qname resolves a QName written in a schema document. An unprefixed name
// belongs to the enclosing schema's target namespace, not to the default
// namespace, so tns is passed separately from the prefix scope.
// qnameAlt returns the primary Clark name plus the fallback for an unprefixed
// name, which some schemas mean as their default namespace rather than their
// target namespace (the xmldsig schema, whose default namespace is XML Schema,
// writes type="string" for xs:string).
func qnameAlt(v, tns string, scope map[string]string) (q, alt string, err error) {
	q, err = qname(v, tns, scope)
	if err != nil {
		return "", "", err
	}
	v = strings.TrimSpace(v)
	if strings.HasPrefix(v, "{") || strings.Contains(v, ":") {
		return q, "", nil // written with a prefix or already resolved
	}
	if d := scope[""]; d != "" && d != tns {
		alt = clark(d, v)
	}
	return q, alt, nil
}

func qname(v, tns string, scope map[string]string) (string, error) {
	v = strings.TrimSpace(v)
	if v == "" {
		return "", fmt.Errorf("empty QName")
	}
	if v[0] == '{' { // Clark notation, as written by the generated model
		return v, nil
	}
	if i := strings.Index(v, ":"); i >= 0 {
		p, l := v[:i], v[i+1:]
		u, ok := scope[p]
		if !ok {
			return "", fmt.Errorf("undeclared namespace prefix %q in %q", p, v)
		}
		return clark(u, l), nil
	}
	return clark(tns, v), nil
}

func local(name string) string {
	_, l := splitClark(name)
	return l
}

// filepathClean normalises a path without importing path/filepath, which would
// drag OS-specific code into a package that only ever handles schema URIs.
func filepathClean(p string) string {
	return path.Clean(p)
}

func parseTree(src []byte) (*xnode, error) {
	dec := xml.NewDecoder(bytes.NewReader(src))
	dec.Entity = xml.HTMLEntity
	root := &xnode{}
	stack := []*xnode{root}
	for {
		tk, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		switch tk := tk.(type) {
		case xml.StartElement:
			n := &xnode{name: clark(tk.Name.Space, tk.Name.Local), attrs: map[string]string{}, xmlns: map[string]string{}}
			for _, a := range tk.Attr {
				switch {
				case a.Name.Space == "" && a.Name.Local == "xmlns":
					n.xmlns[""] = a.Value
				case a.Name.Space == "xmlns":
					n.xmlns[a.Name.Local] = a.Value
				default:
					n.attrs[a.Name.Local] = a.Value
				}
			}
			top := stack[len(stack)-1]
			top.kids = append(top.kids, n)
			stack = append(stack, n)
		case xml.EndElement:
			if len(stack) > 1 {
				stack = stack[:len(stack)-1]
			}
		}
	}
	if len(root.kids) != 1 {
		return nil, fmt.Errorf("expected exactly one root element, got %d", len(root.kids))
	}
	return root.kids[0], nil
}

// SortNames is a helper for error messages: it sorts Clark names by local name.
func sortNames(names []string) {
	sort.Slice(names, func(i, j int) bool {
		_, a := splitClark(names[i])
		_, b := splitClark(names[j])
		if a != b {
			return a < b
		}
		return names[i] < names[j]
	})
}
