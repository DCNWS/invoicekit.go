package xp

import (
	"encoding/xml"
	"errors"
	"io"
	"strings"
	"testing"
)

// TestNodeAttrLookup covers the exported attribute lookup, which a caller
// outside this package uses to read a schema attribute without walking Attrs.
func TestRootElemOfEmptyDoc(t *testing.T) {
	if (&Doc{}).RootElem() != nil {
		t.Error("a document with no root element reports one")
	}
}

func TestNodeAttrLookup(t *testing.T) {
	d := doc(t)
	var b *Node
	for _, n := range d.All {
		if n.Name == "B" {
			b = n
		}
	}
	if b == nil {
		t.Fatal("test document has no B element")
	}
	a, ok := b.Attr("id", "")
	if !ok || a.Value != "7" {
		t.Errorf(`Attr("id", "") = %+v, %v, want value 7`, a, ok)
	}
	if _, ok := b.Attr("id", "urn:ram"); ok {
		t.Error(`Attr("id", "urn:ram") matched, want no match: the attribute is unprefixed`)
	}
	if _, ok := b.Attr("missing", ""); ok {
		t.Error(`Attr("missing", "") matched, want no match`)
	}
}

// TestParseElementStream covers the entry point for a file that wraps several
// invoice documents: each subtree is parsed on its own, with its own namespace
// declarations, and the reader is left positioned after the subtree.
func TestParseElementStream(t *testing.T) {
	const set = `<set>` +
		`<rsm:X xmlns:rsm="urn:rsm"><ram:A xmlns:ram="urn:ram"><udt:V xmlns:udt="urn:udt">1</udt:V></ram:A></rsm:X>` +
		`<rsm:Y xmlns:rsm="urn:rsm" ram:flag="on" xmlns:ram="urn:ram">two</rsm:Y>` +
		`</set>`
	dec := xml.NewDecoder(strings.NewReader(set))
	tok, err := dec.Token()
	if err != nil {
		t.Fatalf("token: %v", err)
	}
	if _, ok := tok.(xml.StartElement); !ok {
		t.Fatalf("first token is %T, want a start element", tok)
	}
	var got []string
	for {
		d, se, err := ParseNextElement(dec)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatalf("ParseNextElement: %v", err)
		}
		root := d.RootElem()
		if root == nil || root.IsDoc {
			t.Fatalf("RootElem of %s is the document node", se.Name.Local)
		}
		got = append(got, se.Name.Local+":"+root.Name)
	}
	if want := "X:X Y:Y"; strings.Join(got, " ") != want {
		t.Errorf("subtrees = %v, want %s", got, want)
	}
}

// TestParseElementDirect feeds the start token that has already been consumed,
// which is how a caller that has sniffed the wrapper's contents hands over the
// invoice element.
func TestParseElementDirect(t *testing.T) {
	dec := xml.NewDecoder(strings.NewReader(`<wrap><rsm:X xmlns:rsm="urn:rsm"><A>hi</A></rsm:X></wrap>`))
	for {
		tok, err := dec.Token()
		if err != nil {
			t.Fatalf("token: %v", err)
		}
		se, ok := tok.(xml.StartElement)
		if !ok {
			continue
		}
		if se.Name.Local != "X" {
			continue
		}
		d, err := ParseElement(dec, se)
		if err != nil {
			t.Fatalf("ParseElement: %v", err)
		}
		if got := d.RootElem().StringValue(); got != "hi" {
			t.Errorf("string value = %q, want hi", got)
		}
		return
	}
}

// TestParseXMLErrors checks that a malformed or namespaced-wrong document is
// rejected with a specific diagnosis instead of being half-parsed. The decoder
// catches an unbalanced end element itself, so that case only pins the message
// the caller sees.
func TestParseXMLErrors(t *testing.T) {
	cases := []struct {
		src, want string
	}{
		{`<a:b/>`, "undefined namespace prefix"},
		{`<a p:x="1"/>`, "undefined namespace prefix"},
		{`<a></a></b>`, "unexpected end element"},
		{`   `, "empty document"},
		{`<a>`, "xml:"},
	}
	for _, c := range cases {
		_, err := ParseXML(c.src)
		if err == nil {
			t.Errorf("ParseXML(%q) succeeded, want %s", c.src, c.want)
			continue
		}
		if !strings.Contains(err.Error(), c.want) {
			t.Errorf("ParseXML(%q) = %v, want it to mention %s", c.src, err, c.want)
		}
	}
}
