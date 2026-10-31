package sch

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dcnws/invoicekit.go/internal/xp"
)

// profDoc is a miniature instance: two ram:A elements, the second carrying two
// values, plus a switch attribute that a global variable reads.
const profDoc = `<rsm:X xmlns:rsm="urn:rsm" xmlns:ram="urn:ram" xmlns:udt="urn:udt" k="on">` +
	`<ram:A><udt:V>1</udt:V></ram:A>` +
	`<ram:A><udt:V>3</udt:V><udt:V>4</udt:V></ram:A>` +
	`</rsm:X>`

const profHead = `<schema xmlns="http://purl.oclc.org/dsdl/schematron"` +
	` xmlns:rsm="urn:rsm" xmlns:ram="urn:ram" xmlns:udt="urn:udt" queryBinding="xslt2">` +
	`<ns prefix="rsm" uri="urn:rsm"/><ns prefix="ram" uri="urn:ram"/><ns prefix="udt" uri="urn:udt"/>`

func parseSrc(t *testing.T, src string) *Schema {
	t.Helper()
	s, err := Parse([]byte(src))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if err := s.Compile(); err != nil {
		t.Fatalf("compile: %v (%v)", err, s.CompileErrors())
	}
	return s
}

func profDocT(t *testing.T) *xp.Doc {
	t.Helper()
	d, err := xp.ParseXML(profDoc)
	if err != nil {
		t.Fatalf("doc: %v", err)
	}
	return d
}

func ids(fs []Finding) []string {
	var out []string
	for _, f := range fs {
		out = append(out, f.ID)
	}
	return out
}

// TestLets pins variable scope. Schema- and pattern-level lets are global, as
// the reference compiler emits them as top-level XSLT variables, so they are
// visible in rule contexts as well as assert tests; later lets may read earlier
// ones. Rule-level lets are relative to the rule's focus node.
func TestLets(t *testing.T) {
	s := parseSrc(t, profHead+
		`<let name="LIMIT" value="'2'"/>`+
		`<let name="LIMIT2" value="number(concat($LIMIT,'0'))"/>`+
		`<pattern id="p">`+
		`<let name="is-target" value="/rsm:X/@k = 'on'"/>`+
		`<rule context="ram:A[$is-target]">`+
		`<let name="v-path" value="udt:V"/>`+
		`<let name="many" value="count($v-path) > 1"/>`+
		`<assert test="not($many)" id="A1">too many values</assert>`+
		`<assert test="number(udt:V[1]) &lt;= number($LIMIT)" id="A2">over limit</assert>`+
		`<assert test="$LIMIT2 = 20" id="A3">bad global</assert>`+
		`</rule></pattern></schema>`)
	fs, err := s.Validate(profDocT(t), nil)
	if err != nil {
		t.Fatalf("validate: %v", err)
	}
	if got := strings.Join(ids(fs), ","); got != "A1,A2" {
		t.Errorf("findings = %v (%v), want A1,A2", got, fs)
	}
}

// TestInclude resolves <include href> through a caller-supplied resolver. The
// KoSIT artefacts include a file whose root element is a bare <pattern>.
func TestInclude(t *testing.T) {
	main := profHead + `<include href="../common.sch"/>` +
		`<pattern id="p"><rule context="rsm:X"><assert test="$GREETING = 'hi'" id="A1">x</assert></rule></pattern></schema>`
	seen := ""
	s, err := ParseWith([]byte(main), func(href string) ([]byte, error) {
		seen = href
		return []byte(`<pattern xmlns="http://purl.oclc.org/dsdl/schematron" id="vars">` +
			`<let name="GREETING" value="'hi'"/></pattern>`), nil
	})
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if seen != "../common.sch" {
		t.Errorf("resolver saw %q, want ../common.sch", seen)
	}
	fs, err := s.Validate(profDocT(t), nil)
	if err != nil {
		t.Fatalf("validate: %v", err)
	}
	if len(fs) != 0 {
		t.Errorf("findings = %v, want none", ids(fs))
	}
	if _, err := Parse([]byte(main)); err == nil {
		t.Error("an include with no resolver must be an error")
	}
}

// TestXSLFunctions calls <xsl:function> bodies from an assert. The bodies are
// plain XPath with xsl:param/xsl:variable/xsl:sequence, and the u: prefix is
// declared only as an xmlns on the root element (no <ns> child), which is how
// the XRechnung artefacts spell their utils functions.
func TestXSLFunctions(t *testing.T) {
	s := parseSrc(t, `<schema xmlns="http://purl.oclc.org/dsdl/schematron"`+
		` xmlns:xsl="http://www.w3.org/1999/XSL/Transform" xmlns:xr="xrechnung" xmlns:u="utils"`+
		` xmlns:rsm="urn:rsm" xmlns:ram="urn:ram" xmlns:udt="urn:udt" queryBinding="xslt2">`+
		`<ns prefix="rsm" uri="urn:rsm"/><ns prefix="ram" uri="urn:ram"/><ns prefix="udt" uri="urn:udt"/>`+
		`<xsl:function name="xr:even" as="xs:boolean">`+
		`<xsl:param name="n" as="xs:string"/>`+
		`<xsl:variable name="d" select="xs:integer($n) mod 2"/>`+
		`<xsl:sequence select="$d = 0"/></xsl:function>`+
		`<xsl:function as="xs:decimal" name="u:orZero">`+
		`<xsl:param name="e"/>`+
		`<xsl:sequence select="if (boolean($e)) then xs:decimal($e) else 0"/></xsl:function>`+
		`<pattern id="p"><rule context="ram:A">`+
		`<assert test="xr:even(udt:V[1])" id="E1">odd</assert>`+
		`<assert test="u:orZero(ram:Missing) = 0" id="Z1">not zero</assert>`+
		`</rule></pattern></schema>`)
	fs, err := s.Validate(profDocT(t), nil)
	if err != nil {
		t.Fatalf("validate: %v", err)
	}
	// Both ram:A contexts hold an odd first value, so the assertion fires once
	// per context; u:orZero over a missing element yields the empty sequence,
	// which is false, so Z1 must stay quiet.
	if got := strings.Join(ids(fs), ","); got != "E1,E1" {
		t.Errorf("findings = %v (%v), want E1,E1", got, fs)
	}
	for _, f := range fs {
		if f.Err != "" {
			t.Errorf("%s: the body should have evaluated, got error %q", f.ID, f.Err)
		}
	}
}

// TestFunctionBodies covers the body shapes the Peppol BIS helpers use inside an
// extension function: a <function> that switches the default namespace to XSLT
// (so its element name carries no prefix), a variable whose value is element
// content rather than a select attribute, and <xsl:choose> with several
// <xsl:when> branches and an <xsl:otherwise>.
func TestFunctionBodies(t *testing.T) {
	s := parseSrc(t, profHead+`<ns prefix="u" uri="utils"/>`+
		`<function xmlns="http://www.w3.org/1999/XSL/Transform" name="u:sign" as="xs:string">`+
		`<param name="n"/>`+
		`<variable name="digits">0123456789</variable>`+
		`<choose>`+
		`<when test="number($n) gt 0"><sequence select="'+'"/></when>`+
		`<when test="contains($digits, substring($n, 1, 1))"><sequence select="'digit'"/></when>`+
		`<otherwise><sequence select="'-'"/></otherwise>`+
		`</choose>`+
		`</function>`+
		`<function xmlns="http://www.w3.org/1999/XSL/Transform" name="u:neg" as="xs:string">`+
		`<param name="n"/>`+
		`<choose><when test="false()">`+
		`<choose><when test="true()"><sequence select="'inner'"/></when></choose>`+
		`</when><otherwise><sequence select="'-'"/></otherwise></choose>`+
		`</function>`+
		`<pattern id="p"><rule context="ram:A">`+
		`<assert test="u:sign(udt:V[1]) = '+'" id="S1">not positive</assert>`+
		`<assert test="u:sign('0') = 'digit'" id="S2">not a digit</assert>`+
		`<assert test="u:sign('x') = u:neg('x')" id="S3">branches disagree</assert>`+
		`<assert test="u:sign('x') = 'bogus'" id="S4">the body never ran</assert>`+
		`</rule></pattern></schema>`)
	fs, err := s.Validate(profDocT(t), nil)
	if err != nil {
		t.Fatalf("validate: %v", err)
	}
	// Two ram:A contexts, so the deliberately false assertion fires twice and
	// proves the choose body was really evaluated instead of assumed true.
	if got := strings.Join(ids(fs), ","); got != "S4,S4" {
		t.Errorf("findings = %v, want S4,S4", ids(fs))
	}
	for _, f := range fs {
		if f.Err != "" {
			t.Errorf("%s: the body should have evaluated, got error %q", f.ID, f.Err)
		}
	}
}

// TestFunctionVariableSelect pins the select attribute of a local <variable>
// inside a function. A dropped select becomes the empty sequence without any
// error: the body still runs, it simply answers a different question. That is
// how the XRechnung IBAN helper ended up calling every IBAN, valid or not,
// wrong.
func TestFunctionVariableSelect(t *testing.T) {
	s := parseSrc(t, profHead+`<ns prefix="u" uri="utils"/>`+
		`<function xmlns="http://www.w3.org/1999/XSL/Transform" name="u:double" as="xs:string">`+
		`<param name="in"/>`+
		`<variable name="up" select="upper-case($in)"/>`+
		`<variable name="cat" select="concat($up, '|', $up)"/>`+
		`<sequence select="$cat"/>`+
		`</function>`+
		`<pattern id="p"><rule context="ram:A">`+
		`<assert test="u:double(udt:V[1]) = concat(upper-case(udt:V[1]), '|', upper-case(udt:V[1]))" id="W1">the variable lost its value</assert>`+
		`<assert test="u:double('a') = 'A|A'" id="W2">the argument was lost</assert>`+
		`</rule></pattern></schema>`)
	fs, err := s.Validate(profDocT(t), nil)
	if err != nil {
		t.Fatalf("validate: %v", err)
	}
	if got := strings.Join(ids(fs), ","); got != "" {
		t.Errorf("findings = %v, want none", got)
	}
}

// TestReportAndMessages checks that <report> fires when its test is true (the
// opposite of <assert>) and that <name/> and <value-of/> inside an assert body
// expand against the rule context, which is what the CIUS messages rely on.
func TestReportAndMessages(t *testing.T) {
	s := parseSrc(t, profHead+`<let name="LIMIT" value="'2'"/>`+
		`<pattern id="p"><rule context="ram:A">`+
		`<assert test="false()" id="A1">bad <name/> code <value-of select="$LIMIT"/></assert>`+
		`<report test="count(udt:V) &gt; 1" id="R1">several in <name/></report>`+
		`</rule></pattern></schema>`)
	fs, err := s.Validate(profDocT(t), nil)
	if err != nil {
		t.Fatalf("validate: %v", err)
	}
	if got := strings.Join(ids(fs), ","); got != "A1,A1,R1" {
		t.Fatalf("findings = %v, want A1,A1,R1", ids(fs))
	}
	if fs[0].Message != "bad ram:A code 2" {
		t.Errorf("message = %q, want %q", fs[0].Message, "bad ram:A code 2")
	}
	if fs[2].Message != "several in ram:A" {
		t.Errorf("report message = %q, want %q", fs[2].Message, "several in ram:A")
	}
}

// TestStrictElements makes sure an element this reader does not understand is
// an error: silently skipping it would let a profile artefact's rules be
// dropped without anyone noticing.
func TestStrictElements(t *testing.T) {
	for _, body := range []string{`<bogus/>`, `<xsl:if xmlns:xsl="http://www.w3.org/1999/XSL/Transform" test="1"/>`} {
		if _, err := Parse([]byte(profHead + body + `</schema>`)); err == nil {
			t.Errorf("parse of %s succeeded, want an error", body)
		}
	}
}

// profileDir is the upstream XRechnung Schematron checkout, if it is present
// beside this repository. Reading it unmodified is the acceptance test for the
// profile features above: includes, global lets, extension functions and the
// prefixes that appear only as xmlns declarations on the root element.
const profileDir = "../../../ref-xrechnung/xrechnung-schematron-master/src/validation/schematron"

func TestRealProfileArtefacts(t *testing.T) {
	cases := []struct {
		file         string
		rules, assts int
		funcs        int
	}{
		{"cii/XRechnung-CII-validation.sch", 27, 52, 2},
		{"ubl/XRechnung-UBL-validation.sch", 25, 56, 1},
	}
	for _, tc := range cases {
		data, err := os.ReadFile(filepath.Join(profileDir, tc.file))
		if err != nil {
			t.Skipf("profile artefacts not available: %v", err)
		}
		dir := filepath.Dir(tc.file)
		s, err := ParseWith(data, func(href string) ([]byte, error) {
			return os.ReadFile(filepath.Join(profileDir, dir, href))
		})
		if err != nil {
			t.Fatalf("parse %s: %v", tc.file, err)
		}
		if err := s.Compile(); err != nil {
			t.Fatalf("compile %s: %v", tc.file, err)
		}
		for _, e := range s.CompileErrors() {
			t.Errorf("%s: %s", tc.file, e)
		}
		rules, assts := s.Stats()
		if rules != tc.rules || assts != tc.assts {
			t.Errorf("%s: %d rules/%d asserts, want %d/%d",
				tc.file, rules, assts, tc.rules, tc.assts)
		}
		if len(s.Funcs) != tc.funcs {
			t.Errorf("%s: functions %v, want the two declared helpers", tc.file, s.Funcs)
		}
		if len(s.Phases) != 1 || len(s.Phases[0].Patterns) != 4 {
			t.Errorf("%s: phases %+v, want one phase with four patterns", tc.file, s.Phases)
		}
	}
}
