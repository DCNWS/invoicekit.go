package sch

import (
	"strings"
	"testing"

	"github.com/dcnws/invoicekit.go/internal/xp"
)

const probeDoc = `<Root><A>1</A><B>x</B></Root>`

func doc(t *testing.T) *xp.Doc {
	t.Helper()
	d, err := xp.ParseXML(probeDoc)
	if err != nil {
		t.Fatal(err)
	}
	return d
}

// A schema is only worth its verdict if it compiled. Compile collects every
// expression it cannot parse instead of stopping at the first one, because one
// broken helper should not hide how much of the rule set is affected — and
// Validate must refuse the schema rather than score it.
func TestCompileCollectsEveryError(t *testing.T) {
	cases := []struct{ name, src, want string }{
		{"undeclared function prefix",
			`<schema xmlns:xsl="` + xslNS + `"><xsl:function name="f:z"><xsl:sequence select="1"/></xsl:function><pattern id="p"/></schema>`,
			"extension function f:z must be in a declared namespace"},
		{"function body",
			`<schema><ns prefix="f" uri="urn:f"/><xsl:function xmlns:xsl="` + xslNS + `" name="f:z"><xsl:sequence select="1 +"/></xsl:function><pattern id="p"/></schema>`,
			"function f:z:"},
		{"global let",
			`<schema><let name="g" value="(("/><pattern id="p"/></schema>`,
			"let $g:"},
		{"rule context",
			`<schema><pattern id="q"><rule context=".["><assert id="A" test="true()">m</assert></rule></pattern></schema>`,
			`pattern q: rule ".[": context:`},
		{"rule let",
			`<schema><pattern id="q"><rule context="/"><let name="v" value="foo["/><assert id="A" test="true()">m</assert></rule></pattern></schema>`,
			`pattern q: rule "/": let $v:`},
		{"assert test",
			`<schema><pattern id="q"><rule context="/"><assert id="A" test="1 +">m</assert></rule></pattern></schema>`,
			"pattern q: assert A: test:"},
		{"report test",
			`<schema><pattern id="q"><rule context="/"><report id="R" test="1 +">m</report></rule></pattern></schema>`,
			"pattern q: report R: test:"},
		{"value-of in message",
			`<schema><pattern id="q"><rule context="/"><assert id="A" test="true()">m<value-of select="foo["/></assert></rule></pattern></schema>`,
			"pattern q: assert A: message:"},
		{"name with a path",
			`<schema><pattern id="q"><rule context="/"><assert id="A" test="true()">m<name path="((" /></assert></rule></pattern></schema>`,
			"pattern q: assert A: message:"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s, err := Parse([]byte(tc.src))
			if err != nil {
				t.Fatal(err)
			}
			err = s.Compile()
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("Compile: %v, want it to mention %q", err, tc.want)
			}
			if len(s.CompileErrors()) == 0 {
				t.Error("CompileErrors is empty although Compile reported a failure")
			}
			if _, err := s.Validate(doc(t), nil); err == nil {
				t.Error("Validate accepted a schema that does not compile")
			}
		})
	}
}

// Compiling twice must not duplicate the collected errors or rebind the
// functions twice, and a second call is a no-op once the schema is compiled.
func TestCompileIsIdempotent(t *testing.T) {
	s, err := Parse([]byte(`<schema><let name="g" value="1"/><pattern id="p"><rule context="/"><assert id="A" test="true()">m</assert></rule></pattern></schema>`))
	if err != nil {
		t.Fatal(err)
	}
	for i := range 3 {
		if err := s.Compile(); err != nil {
			t.Fatalf("Compile %d: %v", i+1, err)
		}
	}
	if bad := s.CompileErrors(); len(bad) != 0 {
		t.Errorf("CompileErrors after recompiles: %v", bad)
	}
	fs, err := s.Validate(doc(t), nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(fs) != 0 {
		t.Errorf("findings: %v", fs)
	}
}

// A phase that is not in the file is a mistake in the caller or in the rule set;
// either way the answer has to name the phases that do exist.
func TestValidateUnknownPhase(t *testing.T) {
	s, err := Parse([]byte(`<schema><phase id="one"><active pattern="p"/></phase><pattern id="p"><rule context="/"><assert id="A" test="false()">m</assert></rule></pattern></schema>`))
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.Validate(doc(t), []string{"nope"})
	if err == nil || !strings.Contains(err.Error(), `unknown phase "nope" (have [one])`) {
		t.Fatalf("got %v", err)
	}
	fs, err := s.Validate(doc(t), []string{"one"})
	if err != nil {
		t.Fatal(err)
	}
	if len(fs) != 1 || fs[0].ID != "A" {
		t.Errorf("findings: %+v", fs)
	}
}

// Expressions that parse but cannot be evaluated are findings, not dropped
// rules: a validator that stays quiet about its own failure looks like a clean
// document. Every one of them carries the expression that failed.
func TestValidateReportsEvaluationFailures(t *testing.T) {
	cases := []struct{ name, src, id, msg, err string }{
		{"global let",
			`<schema><let name="g" value="$nope"/><pattern id="p"><rule context="/"><assert id="A" test="true()">m</assert></rule></pattern></schema>`,
			"SCH-LET", "global variable could not be evaluated", "undefined variable $nope"},
		{"rule context",
			`<schema><pattern id="p"><rule context="$nope"><assert id="A" test="true()">m</assert></rule></pattern></schema>`,
			"SCH-RULE", "rule context could not be evaluated", "undefined variable $nope"},
		{"rule let",
			`<schema><pattern id="p"><rule context="/Root"><let name="v" value="1 div 0"/><assert id="A" test="true()">m</assert></rule></pattern></schema>`,
			"SCH-LET", "rule variable $v could not be evaluated", "division by zero"},
		{"assert test",
			`<schema><pattern id="p"><rule context="/Root"><assert id="A" flag="warning" test="nonexistent:fn()">m</assert></rule></pattern></schema>`,
			"A", "assert test could not be evaluated", "unknown function nonexistent:fn"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s, err := Parse([]byte(tc.src))
			if err != nil {
				t.Fatal(err)
			}
			fs, err := s.Validate(doc(t), nil)
			if err != nil {
				t.Fatal(err)
			}
			if len(fs) != 1 {
				t.Fatalf("findings: %+v", fs)
			}
			f := fs[0]
			if f.ID != tc.id || f.Message != tc.msg || !strings.Contains(f.Err, tc.err) {
				t.Errorf("finding %+v, want id %s, message %q, err mentioning %q", f, tc.id, tc.msg, tc.err)
			}
		})
	}
}

// A rule whose context selects something that is not a node has nothing to
// assert on: an empty selection must not be mistaken for a pass, and the
// assertions must not run against a value that is not a node.
func TestValidateSkipsWhatItCannotEvaluate(t *testing.T) {
	const src = `<schema><pattern id="p"><rule context="(1)">
		<assert id="A" test="true()">never reached</assert>
		<report id="R" test="nonexistent:fn()">neither</report>
	</rule></pattern></schema>`
	s, err := Parse([]byte(src))
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Compile(); err != nil {
		t.Fatal(err)
	}
	fs, err := s.Validate(doc(t), nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range fs {
		if f.ID == "A" {
			t.Errorf("a context that selects no node asserted %q anyway", f.ID)
		}
	}

	// One broken expression is enough to make the whole schema unscoreable, and
	// saying so beats returning the verdict of the rules that did compile.
	bad, err := Parse([]byte(`<schema><pattern id="p"><rule context="/Root"><assert id="A" test="1 +">m</assert><assert id="B" test="false()">fires</assert></rule></pattern></schema>`))
	if err != nil {
		t.Fatal(err)
	}
	if err := bad.Compile(); err == nil {
		t.Fatal("Compile accepted an uncompilable test")
	}
	if _, err := bad.Validate(doc(t), nil); err == nil {
		t.Error("Validate scored a schema that does not compile")
	}
	// Trying again must not lose the fault: a second Compile reports the same
	// problem instead of pretending the schema is now good.
	if err := bad.Compile(); err == nil {
		t.Error("the second Compile forgot the compile errors")
	}
}

// Message templates carry the evidence: <name/> and <value-of/> expand against
// the failing node, and a part that cannot be evaluated is left out rather than
// replacing the whole message with an error.
func TestAssertMessageRendering(t *testing.T) {
	const src = `<schema><pattern id="p"><rule context="/Root/A">
		<assert id="N" test="false()">node <name/> at <name path="@nope"/></assert>
		<assert id="V" test="false()">value <value-of select="."/></assert>
		<assert id="E" test="false()">bad <value-of select="$nope"/> end</assert>
		<assert id="U" test="false()">gone <value-of select="nonexistent:fn()"/> end</assert>
		<assert id="F" test="false()">plain text</assert>
	</rule></pattern></schema>`
	s, err := Parse([]byte(src))
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Compile(); err != nil {
		t.Fatal(err)
	}
	fs, err := s.Validate(doc(t), nil)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		"N": "node A at",
		"V": "value 1",
		"E": "bad  end",
		"U": "gone  end",
		"F": "plain text",
	}
	if len(fs) != len(want) {
		t.Fatalf("findings: %+v", fs)
	}
	for _, f := range fs {
		w, ok := want[f.ID]
		if !ok {
			t.Errorf("unexpected finding %s", f.ID)
			continue
		}
		if !strings.HasPrefix(f.Message, w) {
			t.Errorf("%s: message %q, want it to start with %q", f.ID, f.Message, w)
		}
	}
}
