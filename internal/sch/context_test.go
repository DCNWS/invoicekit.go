package sch

import (
	"testing"

	"github.com/dcnws/invoicekit.go/internal/xp"
)

// contextTestHead declares the namespaces both the rule set and the instance
// use, without a root-element test so that only the rule context decides.
const contextTestHead = `<schema xmlns="http://purl.oclc.org/dsdl/schematron" queryBinding="xslt2"` +
	` xmlns:ubl-invoice="urn:oasis:names:specification:ubl:schema:xsd:Invoice-2"` +
	` xmlns:ubl-creditnote="urn:oasis:names:specification:ubl:schema:xsd:CreditNote-2"` +
	` xmlns:cbc="urn:oasis:names:specification:ubl:schema:xsd:CommonBasicComponents-2">` +
	`<ns prefix="ubl-invoice" uri="urn:oasis:names:specification:ubl:schema:xsd:Invoice-2"/>` +
	`<ns prefix="ubl-creditnote" uri="urn:oasis:names:specification:ubl:schema:xsd:CreditNote-2"/>` +
	`<ns prefix="cbc" uri="urn:oasis:names:specification:ubl:schema:xsd:CommonBasicComponents-2"/>`

const contextTestDoc = `<ubl-invoice:Invoice xmlns:ubl-invoice="urn:oasis:names:specification:ubl:schema:xsd:Invoice-2"` +
	` xmlns:cbc="urn:oasis:names:specification:ubl:schema:xsd:CommonBasicComponents-2">` +
	`<cbc:ID>1</cbc:ID></ubl-invoice:Invoice>`

// TestRuleContextAtRoot checks that a rule context naming the document's root
// element selects it, whichever way the schema spells the context. A relative
// context is anchored at descendant-or-self::node() from the root of the tree,
// which is how the reference implementation's XSLT templates behave, so the
// root element is in scope and not only its descendants.
func TestRuleContextAtRoot(t *testing.T) {
	cases := []struct {
		ctx  string
		want int
	}{
		{"ubl-invoice:Invoice", 1},
		{"ubl-invoice:Invoice | ubl-creditnote:CreditNote", 1},
		{"ubl-creditnote:CreditNote | ubl-invoice:Invoice", 1},
		{"/*[self::ubl-invoice:Invoice or self::ubl-creditnote:CreditNote]", 1},
		{"ubl-creditnote:CreditNote", 0},
		{"cbc:ID", 1},                           // a relative context still finds the child
		{"ubl-creditnote:CreditNote/cbc:ID", 0}, // and stays empty when nothing matches
	}
	for _, c := range cases {
		src := contextTestHead + `<pattern><rule context="` + c.ctx +
			`"><assert id="A1" test="cbc:Missing">missing</assert></rule></pattern></schema>`
		s := parseSrc(t, src)
		d, err := xp.ParseXML(contextTestDoc)
		if err != nil {
			t.Fatalf("parse instance: %v", err)
		}
		fs, err := s.Validate(d, nil)
		if err != nil {
			t.Fatalf("validate context %q: %v", c.ctx, err)
		}
		for _, f := range fs {
			if f.Err != "" {
				t.Errorf("context %q: %s could not be evaluated: %s", c.ctx, f.Test, f.Err)
			}
		}
		if len(fs) != c.want {
			t.Errorf("context %q selected the root %d times, want %d", c.ctx, len(fs), c.want)
		}
	}
}
