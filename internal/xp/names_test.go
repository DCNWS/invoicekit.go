package xp

import "testing"

const namesDoc = `<r xmlns:ram="urn:ram" xmlns:xml="http://www.w3.org/XML/1998/namespace" ram:k="1" xml:lang="en" plain="p">` +
	`<ram:e a="x">t</ram:e></r>`

var namesNS = map[string]string{"ram": "urn:ram", "xml": "http://www.w3.org/XML/1998/namespace"}

func TestNamesAndNamespaceURIs(t *testing.T) {
	d, err := ParseXML(namesDoc)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	cases := []struct {
		src  string
		want string
	}{
		// An attribute keeps the prefix it was written with.
		{`name(/r/@ram:k)`, "ram:k"},
		{`local-name(/r/@ram:k)`, "k"},
		{`prefix-from-QName(/r/@ram:k)`, "ram"},
		{`namespace-uri(/r/@ram:k)`, "urn:ram"},
		{`name(/r/@xml:lang)`, "xml:lang"},
		{`local-name(/r/@xml:lang)`, "lang"},
		{`namespace-uri(/r/@xml:lang)`, "http://www.w3.org/XML/1998/namespace"},
		// An unprefixed attribute is in no namespace, whatever its element is.
		{`name(/r/@plain)`, "plain"},
		{`prefix-from-QName(/r/@plain)`, ""},
		{`namespace-uri(/r/@plain)`, ""},
		{`name(//ram:e/@*)`, "a"},
		{`namespace-uri(//ram:e/@*)`, ""},
		// Elements take the prefix that declares their namespace.
		{`name(//ram:e)`, "ram:e"},
		{`local-name(//ram:e)`, "e"},
		{`prefix-from-QName(//ram:e)`, "ram"},
		{`namespace-uri(//ram:e)`, "urn:ram"},
		{`name(/)`, ""},
		{`namespace-uri(/)`, ""},
		{`name(//nope)`, ""},
		{`namespace-uri(//nope)`, ""},
	}
	for _, c := range cases {
		e, err := Compile(c.src, namesNS)
		if err != nil {
			t.Fatalf("compile %s: %v", c.src, err)
		}
		v, err := e.Eval(d, d.Root)
		if err != nil {
			t.Fatalf("eval %s: %v", c.src, err)
		}
		if got := stringOf(v); got != c.want {
			t.Errorf("%s: got %q, want %q", c.src, got, c.want)
		}
	}
}
