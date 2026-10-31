// Package xsd checks XML Schema validity for the two EN 16931 syntaxes.
//
// The official EN 16931 Schematron rule sets check business rules and element
// presence, but they never check element order or whether an element name is
// legal at a given position: the CEF syntax patterns are all of the form
// not(ram:X) or count(ram:X) <= 1. A document can therefore satisfy every
// schematron assert and still be rejected by a receiving system that validates
// against the XSD. This package closes that gap.
//
// It is not a general XSD implementation. It reads the dialect actually used by
// OASIS UBL 2.1 and UN/CEFACT CII D16B - sequences, choices, element
// declarations by name or ref, named and inline complex types, complexContent
// extension, imports and includes - and treats every simple type as an opaque
// leaf. Datatypes, facets and attributes are not checked: the Codes schematron
// patterns cover code values, and leaf text is what the business rules read.
//
// The full schema set is 2.9 MB of XML, most of it documentation. cmd/xsdgen
// prunes it to the reachable component set, drops annotations, and writes a
// compact model that the runtime parses instead (a few hundred KB).
package xsd

// Kind is the kind of a content-model term.
type Kind uint8

const (
	// Elem accepts one element by name.
	Elem Kind = iota
	// Seq is xs:sequence: parts in order.
	Seq
	// Choice is xs:choice: exactly one part.
	Choice
	// Ref is a placeholder for another type's content (complexContent base).
	// The resolver substitutes or drops it; it never survives into a model.
	Ref
	// Any accepts any element; its content is not checked.
	Any
)

func (k Kind) String() string {
	switch k {
	case Elem:
		return "element"
	case Seq:
		return "sequence"
	case Choice:
		return "choice"
	case Ref:
		return "ref"
	case Any:
		return "any"
	}
	return "?"
}

// Term is one node of a content model.
type Term struct {
	Kind Kind
	Min  int // minOccurs
	Max  int // maxOccurs, -1 for unbounded

	// Parts holds the children of a Seq, Choice or Ref term.
	Parts []*Term

	// Name is the accepted element name in Clark notation ({ns}local).
	Name string
	// DeclRef and TypeRef are Clark names awaiting resolution.
	DeclRef string
	TypeRef string
	// TypeAlt is the fallback for an unprefixed type reference when the
	// target namespace has no such component: xmldsig writes type="string"
	// inside a schema whose default namespace is XML Schema.
	TypeAlt string
	// Decl and Type are filled in by the resolver; either may be nil, which
	// means the accepted element is a leaf with no element children.
	Decl *Decl
	Type *Type
	// AnyNS is the namespace constraint of an xs:any.
	AnyNS string
}

// Content returns the element content model this term accepts, or nil when the
// element is a simple value that must not contain child elements.
func (t *Term) Content() *Term {
	if t == nil {
		return nil
	}
	if t.Kind == Ref {
		if t.Type != nil {
			return t.Type.Term
		}
		return nil
	}
	if t.Type != nil {
		return t.Type.Term
	}
	return nil
}

// Type is a complex type: a named content model, or a leaf.
type Type struct {
	ID   string // Clark name; "" for an inline (anonymous) type
	NS   string
	Term *Term // nil means the type has no element children

	// Compilation cache for the matcher.
	frag *frag
}

// Decl is a global element declaration.
type Decl struct {
	ID      string
	NS      string
	TypeRef string
	TypeAlt string
	Type    *Type // nil means the declared element is a leaf
}

// Model is a resolved set of schema components.
type Model struct {
	Decls map[string]*Decl
	Types map[string]*Type

	// Roots lists the global element declarations of the model, in the order
	// they were read; a pruned model lists the roots it was pruned to. Any of
	// them may start a document, which is what Validate does: it looks the
	// document element up in Decls.
	Roots []string
	// Source records where the model came from, for error messages.
	Source string
}

// NewModel returns an empty model.
func NewModel() *Model {
	return &Model{Decls: map[string]*Decl{}, Types: map[string]*Type{}}
}

// clark builds the Clark notation of a name.
func clark(ns, local string) string {
	if ns == "" {
		return local
	}
	return "{" + ns + "}" + local
}

// splitClark splits a Clark name back into namespace and local name.
func splitClark(id string) (ns, local string) {
	if len(id) == 0 || id[0] != '{' {
		return "", id
	}
	for i := 1; i < len(id); i++ {
		if id[i] == '}' {
			return id[1:i], id[i+1:]
		}
	}
	return "", id
}
