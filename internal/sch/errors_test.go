package sch

import (
	"errors"
	"strings"
	"testing"
)

// The reader refuses anything it does not implement instead of skipping it: a
// silently dropped pattern reads as a clean document, which is the failure mode
// a validator must never have. Each case pins one of those refusals, so a
// refactor that quietly starts accepting an element has to say so here.
func TestParseRejectsBadRuleFiles(t *testing.T) {
	cases := []struct {
		name string
		src  string
		want string
	}{
		{"empty file", "", "truncated rule file"},
		{"unclosed schema", `<schema>`, "unexpected EOF"},
		{"malformed xml", `<schema><</schema>`, "sch: xml:"},
		{"no patterns", `<schema></schema>`, "no patterns found"},
		{"wrong root", `<foo/>`, "unexpected root element foo"},
		{"namespaced root", `<x:foo xmlns:x="urn:x"/>`, "unexpected root element {urn:x}foo"},
		{"xsl non-function", `<schema><xsl:variable xmlns:xsl="` + xslNS + `"/></schema>`,
			"variable is not supported at schema level"},
		{"foreign element", `<schema><foo:bar xmlns:foo="urn:foo"/></schema>`,
			"bar is not supported at schema level"},
		{"unknown schematron element", `<schema><bogus/></schema>`,
			"bogus is not supported at schema level"},
		{"ns without uri", `<schema><ns prefix="c"/></schema>`, "<ns> needs prefix and uri"},
		{"let without name", `<schema><let value="1"/></schema>`, "<let> without name"},
		{"let without value", `<schema><let name="x"/></schema>`, "without value"},
		{"active at schema level", `<schema><active pattern="p"/></schema>`, "<active> outside <phase>"},
		{"rule at schema level", `<schema><rule context="/"/></schema>`, "<rule> outside <pattern>"},
		{"assert at schema level", `<schema><assert test="true()">m</assert></schema>`,
			"<assert> outside <rule>"},
		{"report at schema level", `<schema><report test="true()">m</report></schema>`,
			"<report> outside <rule>"},
		{"include without href", `<schema><include/></schema>`, "<include> without href"},
		{"include without resolver", `<schema><include href="other.sch"/></schema>`,
			`includes "other.sch" but no resolver was given`},
		{"foreign element in phase",
			`<schema><phase id="p"><foo:bar xmlns:foo="urn:foo"/></phase><pattern id="q"/></schema>`,
			"bar is not supported in <phase>"},
		{"unknown element in phase",
			`<schema><phase id="p"><bogus/></phase><pattern id="q"/></schema>`,
			"bogus is not supported in <phase>"},
		{"bad let in phase",
			`<schema><phase id="p"><let name="a"/></phase><pattern id="q"/></schema>`,
			"without value"},
		{"truncated phase", `<schema><phase id="p">`, "unexpected EOF"},
		{"foreign element in pattern",
			`<schema><pattern id="p"><foo:bar xmlns:foo="urn:foo"/></pattern></schema>`,
			"bar is not supported in <pattern>"},
		{"unknown element in pattern",
			`<schema><pattern id="p"><bogus/></pattern></schema>`,
			"bogus is not supported in <pattern>"},
		{"bad let in pattern",
			`<schema><pattern id="p"><let name="a"/></pattern></schema>`,
			"without value"},
		{"assert in pattern",
			`<schema><pattern id="p"><assert test="true()">m</assert></pattern></schema>`,
			"<assert> outside <rule>"},
		{"truncated pattern", `<schema><pattern id="p">`, "unexpected EOF"},
		{"rule without context", `<schema><pattern id="p"><rule/></pattern></schema>`,
			"<rule> without context"},
		{"foreign element in rule",
			`<schema><pattern id="p"><rule context="/"><foo:bar xmlns:foo="urn:foo"/></rule></pattern></schema>`,
			"bar is not supported in <rule>"},
		{"unknown element in rule",
			`<schema><pattern id="p"><rule context="/"><bogus/></rule></pattern></schema>`,
			"bogus is not supported in <rule>"},
		{"bad let in rule",
			`<schema><pattern id="p"><rule context="/"><let name="a"/></rule></pattern></schema>`,
			"without value"},
		{"truncated rule", `<schema><pattern id="p"><rule context="/">`, "unexpected EOF"},
		{"assert without test",
			`<schema><pattern id="p"><rule context="/"><assert>m</assert></rule></pattern></schema>`,
			"<assert> without test"},
		{"report without test",
			`<schema><pattern id="p"><rule context="/"><report>m</report></rule></pattern></schema>`,
			"<report> without test"},
		{"value-of without select",
			`<schema><pattern id="p"><rule context="/"><assert test="1()">m<value-of/></assert></rule></pattern></schema>`,
			"<value-of> without select"},
		{"unsupported message part",
			`<schema><pattern id="p"><rule context="/"><assert test="1()">m<bogus/></assert></rule></pattern></schema>`,
			"bogus is not supported in an assertion message"},
		{"truncated message",
			`<schema><pattern id="p"><rule context="/"><assert test="1()">m`, "unexpected EOF"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s, err := Parse([]byte(tc.src))
			if err == nil {
				t.Fatalf("parsed %d patterns, want an error mentioning %q", len(s.Patterns), tc.want)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error %q, want it to mention %q", err, tc.want)
			}
		})
	}
}

// Every documented shape that is documentation only must still be consumed
// cleanly: rejecting <title> or <p> would fail every real rule file, and
// skipping them without advancing the decoder would misread the next element.
func TestParseAcceptsDocumentationAndDeclarations(t *testing.T) {
	const src = `<schema>
		<title>Test schema</title>
		<p>prose</p><dir>a directory</dir>
		<ns prefix="c" uri="urn:c"/>
		<let name="top" value="1"/>
		<phase id="p"><title>a phase</title><p>why</p><active pattern="q"/><let name="pv" value="2"/></phase>
		<pattern id="q"><title>a pattern</title><p>why</p>
			<let name="pv2" value="3"/>
			<rule context="/">
				<title>a rule</title><p>why</p>
				<let name="rv" value="4"/>
				<assert id="A1" test="true()">message</assert>
				<report id="A2" flag="warning" test="false()">never</report>
			</rule>
		</pattern>
		<include href="nothing.sch"/>
	</schema>`
	res := func(href string) ([]byte, error) {
		if href != "nothing.sch" {
			return nil, errors.New("unexpected include " + href)
		}
		return []byte(`<pattern id="r"><rule context="/"><assert id="B" test="true()">m</assert></rule></pattern>`), nil
	}
	s, err := ParseWith([]byte(src), res)
	if err != nil {
		t.Fatal(err)
	}
	if s.Title != "Test schema" {
		t.Errorf("title %q", s.Title)
	}
	if s.NS["c"] != "urn:c" {
		t.Errorf("ns: %v", s.NS)
	}
	if len(s.Patterns) != 2 || len(s.Phases) != 1 {
		t.Fatalf("got %d patterns / %d phases", len(s.Patterns), len(s.Phases))
	}
	if got := s.Phases[0].Patterns; len(got) != 1 || got[0] != "q" {
		t.Errorf("phase actives: %v", got)
	}
	if len(s.Phases[0].Lets) != 1 || len(s.Lets) != 1 {
		t.Errorf("lets: phase %d, schema %d", len(s.Phases[0].Lets), len(s.Lets))
	}
	if a := s.Patterns[0].Rules[0].Asserts[1]; a.Flag != "warning" || !a.Report {
		t.Errorf("report flags: %+v", a)
	}
	if a := s.Patterns[0].Rules[0].Asserts[0]; a.Flag != "fatal" {
		t.Errorf("an assert with no flag defaults to %q, want fatal", a.Flag)
	}
}

// <include> is a rule-file boundary, so its failures are reported as such: an
// unreadable include must not be mistaken for an empty one.
func TestParseIncludeFailures(t *testing.T) {
	t.Run("resolver error", func(t *testing.T) {
		src := `<schema><include href="other.sch"/></schema>`
		_, err := ParseWith([]byte(src), func(string) ([]byte, error) {
			return nil, errors.New("not found")
		})
		if err == nil || !strings.Contains(err.Error(), `include "other.sch": not found`) {
			t.Fatalf("got %v", err)
		}
	})
	t.Run("cycle", func(t *testing.T) {
		src := `<schema><include href="self.sch"/></schema>`
		_, err := ParseWith([]byte(src), func(string) ([]byte, error) { return []byte(src), nil })
		if err == nil || !strings.Contains(err.Error(), "nested more than") {
			t.Fatalf("got %v", err)
		}
	})
	t.Run("included file cannot be parsed", func(t *testing.T) {
		src := `<schema><include href="bad.sch"/></schema>`
		_, err := ParseWith([]byte(src), func(string) ([]byte, error) { return []byte(`<wrong/>`), nil })
		if err == nil || !strings.Contains(err.Error(), "unexpected root element wrong") {
			t.Fatalf("got %v", err)
		}
	})
}

// An <xsl:function> is only useful if its body can be expressed in the XPath
// subset; the shapes that cannot are refused with the name of the function, so
// the reader learns which helper is at fault rather than that something failed.
func TestParseXSLFunctionRejections(t *testing.T) {
	cases := []struct{ name, src, want string }{
		{"without name",
			`<schema><xsl:function xmlns:xsl="` + xslNS + `"><xsl:sequence select="1"/></xsl:function><pattern id="p"/></schema>`,
			"<xsl:function> without name"},
		{"no sequence",
			`<schema><xsl:function name="f:z" xmlns:xsl="` + xslNS + `" xmlns:f="urn:f"><xsl:variable name="a" select="1"/></xsl:function><pattern id="p"/></schema>`,
			"has no <xsl:sequence>"},
		{"literal text with braces",
			`<schema><xsl:function name="f:z" xmlns:xsl="` + xslNS + `" xmlns:f="urn:f">a{b}<xsl:sequence select="1"/></xsl:function><pattern id="p"/></schema>`,
			"literal text with braces in f:z is not supported"},
		{"param without name",
			`<schema><xsl:function name="f:z" xmlns:xsl="` + xslNS + `" xmlns:f="urn:f"><xsl:param/><xsl:sequence select="1"/></xsl:function><pattern id="p"/></schema>`,
			"<xsl:param> without name in f:z"},
		{"param with default",
			`<schema><xsl:function name="f:z" xmlns:xsl="` + xslNS + `" xmlns:f="urn:f"><xsl:param name="a" select="1"/><xsl:sequence select="$a"/></xsl:function><pattern id="p"/></schema>`,
			"with a default value in f:z is not supported"},
		{"param in nested sequence",
			`<schema><xsl:function name="f:z" xmlns:xsl="` + xslNS + `" xmlns:f="urn:f"><xsl:if test="true()"><xsl:param name="a"/><xsl:sequence select="1"/></xsl:if></xsl:function><pattern id="p"/></schema>`,
			"<xsl:param> inside a nested sequence in f:z"},
		{"variable without name",
			`<schema><xsl:function name="f:z" xmlns:xsl="` + xslNS + `" xmlns:f="urn:f"><xsl:variable select="1"/><xsl:sequence select="1"/></xsl:function><pattern id="p"/></schema>`,
			"<xsl:variable> without name in f:z"},
		{"variable with both select and content",
			`<schema><xsl:function name="f:z" xmlns:xsl="` + xslNS + `" xmlns:f="urn:f"><xsl:variable name="a" select="1"><xsl:sequence select="2"/></xsl:variable><xsl:sequence select="1"/></xsl:function><pattern id="p"/></schema>`,
			"has both select and content in f:z"},
		{"foreign instruction",
			`<schema><xsl:function name="f:z" xmlns:xsl="` + xslNS + `" xmlns:f="urn:f"><foo:bar xmlns:foo="urn:foo"/><xsl:sequence select="1"/></xsl:function><pattern id="p"/></schema>`,
			"bar is not supported in <xsl:function name=f:z>"},
		{"unsupported instruction",
			`<schema><xsl:function name="f:z" xmlns:xsl="` + xslNS + `" xmlns:f="urn:f"><xsl:for-each select="."/><xsl:sequence select="1"/></xsl:function><pattern id="p"/></schema>`,
			"for-each is not supported in <xsl:function name=f:z>"},
		{"truncated body",
			`<schema><xsl:function name="f:z" xmlns:xsl="` + xslNS + `" xmlns:f="urn:f">`,
			"unexpected EOF"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Parse([]byte(tc.src))
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("got %v, want it to mention %q", err, tc.want)
			}
		})
	}
}
