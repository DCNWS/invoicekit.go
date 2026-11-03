package xp

import "testing"

func TestProbe4(t *testing.T) {
	d, err := ParseXML(`<r xmlns:ram="urn:ram" xmlns:xml="http://www.w3.org/XML/1998/namespace" ram:k="1" xml:lang="en" plain="p"><e a="x"/></r>`)
	if err != nil {
		t.Fatal(err)
	}
	for _, n := range d.All {
		t.Logf("node %s ns=%q prefix=%q attrs=%#v", n.Name, n.NS, n.Prefix, n.Attrs)
	}
	for _, src := range []string{`name(@*)`, `local-name(@*)`, `name(//e/@*)`, `namespace-uri-from-QName(//e/@*)`} {
		e, err := Compile(src, tns)
		if err != nil {
			t.Logf("%-32s compile-err %v", src, err)
			continue
		}
		v, err := e.Eval(d, d.Root)
		t.Logf("%-32s -> %q err=%v", src, stringOf(v), err)
	}
}
