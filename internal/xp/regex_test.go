package xp

import "testing"

// TestRegexClasses pins the XPath-to-RE2 pattern mapping for the escapes a
// Schematron profile actually writes. The XRechnung CIUS helpers strip
// whitespace with replace($x, '([\s])', ”), and a character class nested
// inside a class is legal XPath but not RE2, so a textual rewrite of \s into
// "[ \t\n\r]" silently turns [\s] into a class of brackets and blanks and stops
// matching anything. RE2 understands the escapes itself, nested or not.
func TestRegexClasses(t *testing.T) {
	for _, c := range []struct {
		src  string
		want any
	}{
		{`replace('a b c', '\s', '-')`, "a-b-c"},
		{`replace('a b c', '([\s])', '')`, "abc"},
		{`replace('a1b2', '[\d]', '')`, "ab"},
		{`matches('a b', 'a[\s]b')`, true},
		{`matches('aXb', 'a[\w]b')`, true},
		{`matches('a b', 'a[^\s]b')`, false},
		{`tokenize('a, b', ',\s')`, []any{"a", "b"}},
	} {
		e, err := Compile(c.src, tns)
		if err != nil {
			t.Fatalf("compile %s: %v", c.src, err)
		}
		got, err := e.Eval(doc(t), doc(t).Root)
		if err != nil {
			t.Fatalf("eval %s: %v", c.src, err)
		}
		if len(seqOf(got)) != len(seqOf(c.want)) {
			t.Errorf("%s = %v, want %v", c.src, got, c.want)
			continue
		}
		for i, it := range seqOf(got) {
			if stringOf(it) != stringOf(seqOf(c.want)[i]) {
				t.Errorf("%s item %d = %q, want %q", c.src, i, stringOf(it), stringOf(seqOf(c.want)[i]))
			}
		}
	}
}

// TestDotMatchesLineBreaks pins the default dot semantics of XPath regular
// expressions: "." matches any character except a line break unless the "s"
// (dotall) flag is given. Prepending (?s) to every pattern makes fn:tokenize
// split BR-DE-18's Skonto note in the wrong place, because its "#.+#" pattern
// then swallows the line breaks between the entries.
func TestDotMatchesLineBreaks(t *testing.T) {
	for _, c := range []struct {
		src  string
		want any
	}{
		{"tokenize(\"a\nb\", \".\")", []any{"", "\n", ""}},
		{"matches(\"a\nb\", \"a.b\")", false},
		// fn:matches searches rather than matching the whole string, so an
		// unanchored pattern is true wherever it fits. The CIUS regex helpers
		// carry their own ^ and $ when they must cover the whole value.
		{`matches("a12b", "\d\d")`, true},
		{`matches("abc", "b")`, true},
		{"matches(\"a\nb\", \"a.b\", \"s\")", true},
		{"count(tokenize(\"#a#\n#b#\n\", \"#.+#\"))", "3"},
	} {
		e, err := Compile(c.src, tns)
		if err != nil {
			t.Fatalf("compile %s: %v", c.src, err)
		}
		got, err := e.Eval(doc(t), doc(t).Root)
		if err != nil {
			t.Fatalf("eval %s: %v", c.src, err)
		}
		if len(seqOf(got)) != len(seqOf(c.want)) {
			t.Errorf("%s = %v, want %v", c.src, got, c.want)
			continue
		}
		for i, it := range seqOf(got) {
			if stringOf(it) != stringOf(seqOf(c.want)[i]) {
				t.Errorf("%s item %d = %q, want %q", c.src, i, stringOf(it), stringOf(seqOf(c.want)[i]))
			}
		}
	}
}
