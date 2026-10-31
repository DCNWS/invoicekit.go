package einvoice_test

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dcnws/invoicekit.go"
)

// exampleModel decodes one of the shipped example invoice models. Unknown keys
// are refused rather than ignored: an example that silently loses a field
// validates while proving nothing about the field it thought it carried, which
// is how a post code once disappeared from a fixture without anyone noticing.
func exampleModel(t *testing.T, name string) einvoice.Invoice {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("example %s: %v", name, err)
	}
	var inv einvoice.Invoice
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&inv); err != nil {
		t.Fatalf("example %s does not match the model: %v", name, err)
	}
	return inv
}

// TestExampleModels renders every example model the documentation points people
// at and checks the result against the rule set the example claims to follow.
// Each one has to come out clean, or the example is an advertisement for a
// document no network would accept.
func TestExampleModels(t *testing.T) {
	for _, tc := range []struct {
		file string
		sx   einvoice.Syntax
		opt  einvoice.Options
	}{
		{"invoice-model.json", einvoice.SyntaxCII, einvoice.Options{}},
		{"invoice-model.json", einvoice.SyntaxUBL, einvoice.Options{}},
		{"peppol-model.json", einvoice.SyntaxUBL, einvoice.Options{Profile: einvoice.ProfilePeppol}},
		{"peppol-model.json", einvoice.SyntaxCII, einvoice.Options{Profile: einvoice.ProfilePeppol}},
		{"xrechnung-model.json", einvoice.SyntaxCII, einvoice.Options{CIUS: einvoice.CIUSXRechnung}},
		{"xrechnung-model.json", einvoice.SyntaxUBL, einvoice.Options{CIUS: einvoice.CIUSXRechnung}},
	} {
		inv := exampleModel(t, tc.file)
		if err := einvoice.ApplySpec(&inv, tc.opt); err != nil {
			t.Errorf("%s: ApplySpec: %v", tc.file, err)
			continue
		}
		for _, p := range einvoice.Check(inv) {
			t.Errorf("%s: Check: %s %s: %s", tc.file, p.Rule, p.Field, p.Text)
		}
		doc, err := einvoice.Generate(tc.sx, inv)
		if err != nil {
			t.Errorf("%s as %s: Generate: %v", tc.file, tc.sx, err)
			continue
		}
		opt := tc.opt
		opt.Syntax = tc.sx
		res, err := einvoice.Validate(doc, opt)
		if err != nil {
			t.Errorf("%s as %s: Validate: %v", tc.file, tc.sx, err)
			continue
		}
		if res.Internal > 0 {
			t.Errorf("%s as %s: %d rules could not be evaluated", tc.file, tc.sx, res.Internal)
		}
		if len(res.Schema) > 0 {
			t.Errorf("%s as %s: %d structural problem(s): %s", tc.file, tc.sx, len(res.Schema), res.Schema[0].Message)
		}
		if res.Fatal > 0 {
			t.Errorf("%s as %s: %d fatal finding(s): %s", tc.file, tc.sx, res.Fatal, strings.Join(res.IDs(), " "))
		}
	}
}
