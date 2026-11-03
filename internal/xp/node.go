// Package xp implements a small, namespace aware evaluator for the subset of
// XPath 2.0/3.1 used by the official EN 16931 Schematron rule sets.
package xp

import (
	"encoding/xml"
	"fmt"
	"io"
	"maps"
	"strings"
)

// Node is an element (or the document pseudo node) in the parsed tree.
type Node struct {
	Parent   *Node
	Index    int    // index among siblings
	Name     string // local name
	Prefix   string // prefix as written in the document
	NS       string // namespace URI ("" = no namespace)
	Attrs    []Attr
	Children []*Node
	Text     string // direct character data
	IsDoc    bool
	Ord      int // document order position
}

// prefixOf recovers the written prefix for a namespace URI using the scope in
// effect, because encoding/xml hands back the resolved URI rather than the
// prefix.
func prefixOf(cur map[string]string, uri string) string {
	if uri == "" {
		return ""
	}
	for p, u := range cur {
		if u == uri && p != "" && p != "xml" {
			return p
		}
	}
	return ""
}

// xmlNS is the namespace of the xml: prefix, the only one that needs no
// declaration.
const xmlNS = "http://www.w3.org/XML/1998/namespace"

// attrPrefix recovers the prefix an attribute was written with. The encoder
// hands over the resolved namespace URI instead of the prefix, so this looks
// for a prefix bound to that URI and falls back to "xml".
func attrPrefix(cur map[string]string, uri string) string {
	if p := prefixOf(cur, uri); p != "" {
		return p
	}
	if uri == xmlNS {
		return "xml"
	}
	return ""
}

// lookup resolves a prefix, tolerating encoders that already replaced it with
// the namespace URI.
func lookup(cur map[string]string, pfx string) (string, bool) {
	if pfx == "" || pfx == "xmlns" {
		return "", true
	}
	if u, ok := cur[pfx]; ok {
		return u, true
	}
	if strings.Contains(pfx, ":") || strings.Contains(pfx, "urn:") {
		return pfx, true
	}
	return "", false
}

// Attr is an attribute; no namespace unless a prefix was used explicitly.
type Attr struct {
	Name string
	NS   string
	// Prefix is the prefix as written in the document, empty when the
	// attribute is unprefixed. name() and local-name() need it: an attribute
	// keeps the prefix it was written with, unlike an element, whose prefix
	// only carries its namespace.
	Prefix string
	Value  string
}

// StringValue is the string value of a node: the concatenation of all the
// character data inside it.
func (n *Node) StringValue() string {
	var b strings.Builder
	n.writeText(&b)
	return b.String()
}

func (n *Node) writeText(b *strings.Builder) {
	b.WriteString(n.Text)
	for _, c := range n.Children {
		c.writeText(b)
	}
}

// Attr returns the attribute with the given local name and namespace.
func (n *Node) Attr(name, ns string) (Attr, bool) {
	for _, a := range n.Attrs {
		if a.Name == name && a.NS == ns {
			return a, true
		}
	}
	return Attr{}, false
}

// Doc is a parsed document.
type Doc struct {
	Root *Node
	All  []*Node // every node in document order
}

// ParseXML builds the tree, resolving namespace declarations manually because
// Schematron rules match on namespace URIs.
func ParseXML(data string) (*Doc, error) {
	dec := xml.NewDecoder(strings.NewReader(data))
	dec.Strict = true
	return parseWith(dec, nil)
}

// ParseElement parses a single element subtree whose start token has already
// been consumed from dec. It is used for invoice documents embedded in a
// wrapper file such as a test set.
func ParseElement(dec *xml.Decoder, se xml.StartElement) (*Doc, error) {
	return parseWith(dec, &se)
}

// ParseNextElement reads the next element subtree from dec.
func ParseNextElement(dec *xml.Decoder) (*Doc, *xml.StartElement, error) {
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			return nil, nil, io.EOF
		}
		if err != nil {
			return nil, nil, fmt.Errorf("xml: %w", err)
		}
		if se, ok := tok.(xml.StartElement); ok {
			d, err := ParseElement(dec, se)
			return d, &se, err
		}
	}
}

// RootElem returns the outermost element of the document, skipping the
// document node. Rule contexts are evaluated at the document node, but callers
// that want the invoice element itself use this.
func (d *Doc) RootElem() *Node {
	if d.Root.IsDoc && len(d.Root.Children) > 0 {
		return d.Root.Children[0]
	}
	return d.Root
}

func parseWith(dec *xml.Decoder, first *xml.StartElement) (*Doc, error) {
	doc := &Doc{Root: &Node{Name: "#document", IsDoc: true}}
	stack := []*Node{doc.Root}
	nsstack := []map[string]string{{"xml": "http://www.w3.org/XML/1998/namespace"}}
	add := func(n *Node) {
		p := stack[len(stack)-1]
		n.Parent = p
		n.Index = len(p.Children)
		p.Children = append(p.Children, n)
	}
	var pending *xml.StartElement
	if first != nil {
		pending = first
	}
Loop:
	for {
		var tok xml.Token
		if pending != nil {
			tok = *pending
			pending = nil
		} else {
			t, err := dec.Token()
			if err == io.EOF {
				break
			}
			if err != nil {
				return nil, fmt.Errorf("xml: %w", err)
			}
			tok = t
		}
		switch tk := tok.(type) {
		case xml.StartElement:
			local, space := tk.Name.Local, tk.Name.Space
			cur0 := nsstack[len(nsstack)-1]
			ns, ok := lookup(cur0, space)
			if !ok {
				return nil, fmt.Errorf("xml: undefined namespace prefix %q on <%s>", space, local)
			}
			n := &Node{Name: local, NS: ns, Prefix: prefixOf(cur0, ns)}
			cur := maps.Clone(nsstack[len(nsstack)-1])
			var real []Attr
			for _, a := range tk.Attr {
				an, as := a.Name.Local, a.Name.Space
				switch {
				case an == "xmlns" && as == "":
					cur[""] = a.Value
				case as == "xmlns":
					cur[an] = a.Value
				default:
					ans, ok := lookup(cur, as)
					if !ok {
						return nil, fmt.Errorf("xml: undefined namespace prefix %q in attribute %s", as, an)
					}
					real = append(real, Attr{Name: an, NS: ans, Prefix: attrPrefix(cur, ans), Value: a.Value})
				}
			}
			n.Attrs = real
			add(n)
			stack = append(stack, n)
			nsstack = append(nsstack, cur)
		case xml.EndElement:
			if len(stack) == 1 {
				if first != nil {
					break Loop // the requested subtree is complete
				}
				return nil, fmt.Errorf("xml: unbalanced end element %s", tk.Name.Local)
			}
			stack = stack[:len(stack)-1]
			nsstack = nsstack[:len(nsstack)-1]
			if first != nil && len(stack) == 1 {
				break Loop
			}
		case xml.CharData:
			p := stack[len(stack)-1]
			p.Text += string(tk)
		}
	}
	var walk func(*Node)
	walk = func(n *Node) {
		n.Ord = len(doc.All)
		doc.All = append(doc.All, n)
		for _, c := range n.Children {
			walk(c)
		}
	}
	walk(doc.Root)
	if len(doc.Root.Children) == 0 {
		return nil, fmt.Errorf("xml: empty document")
	}
	return doc, nil
}
