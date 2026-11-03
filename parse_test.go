package einvoice_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/dcnws/invoicekit.go"
)

// Parse is the way a document really arrives: bytes, of unknown syntax, with no
// model behind them. These samples are the three the repository keeps, which is
// the honest spread — one real XRechnung, one minimal UBL, one Peppol BIS.
func TestParseReadsEitherSyntax(t *testing.T) {
	tbl := []struct {
		file     string
		syntax   einvoice.Syntax
		id       string
		currency string
		payable  float64
		lines    int
		issue    string
	}{
		{"testdata/samples/cii-xrechnung.xml", einvoice.SyntaxCII, "150377292", "EUR", 385544.60, 2,
			"ram:AdditionalReferencedDocument"},
		{"testdata/samples/ubl-min.xml", einvoice.SyntaxUBL, "2018-112", "SEK", 500, 1, ""},
		{"testdata/samples/ubl-peppol.xml", einvoice.SyntaxUBL, "2026-114", "SEK", 1625, 1, ""},
	}
	for _, tc := range tbl {
		res, err := einvoice.Parse(read(t, tc.file))
		if err != nil {
			t.Errorf("%s: %v", tc.file, err)
			continue
		}
		if res.Syntax != tc.syntax {
			t.Errorf("%s: syntax = %q, want %q", tc.file, res.Syntax, tc.syntax)
		}
		inv := res.Invoice
		if inv.ID != tc.id {
			t.Errorf("%s: id = %q, want %q", tc.file, inv.ID, tc.id)
		}
		if inv.Currency != tc.currency {
			t.Errorf("%s: currency = %q, want %q", tc.file, inv.Currency, tc.currency)
		}
		if p := inv.Totals.Payable; p == nil || *p != tc.payable {
			t.Errorf("%s: payable = %v, want %v", tc.file, p, tc.payable)
		}
		if len(inv.Lines) != tc.lines {
			t.Errorf("%s: %d lines, want %d", tc.file, len(inv.Lines), tc.lines)
		}
		if tc.issue == "" && len(res.Issues) > 0 {
			t.Errorf("%s: issues: %s", tc.file, strings.Join(res.Issues, "; "))
		}
		if tc.issue != "" && !strings.Contains(strings.Join(res.Issues, "\n"), tc.issue) {
			t.Errorf("%s: no issue names %s in %s", tc.file, tc.issue, strings.Join(res.Issues, "; "))
		}
	}
}

// A document that says something the standard has no term for still comes back
// as an invoice, with the part that had nowhere to go named rather than hidden.
func TestParseSaysWhatHadNowhereToGo(t *testing.T) {
	tbl := []struct {
		file, from, to, want string
	}{
		{"testdata/samples/ubl-min.xml", "<cbc:ID>VAT</cbc:ID>", "<cbc:ID>GST</cbc:ID>", "GST"},
		{"testdata/samples/cii-xrechnung.xml", "<ram:TypeCode>VAT</ram:TypeCode>", "<ram:TypeCode>GST</ram:TypeCode>", "GST"},
	}
	for _, tc := range tbl {
		doc := strings.Replace(string(read(t, tc.file)), tc.from, tc.to, 1)
		res, err := einvoice.Parse([]byte(doc))
		if err != nil {
			t.Errorf("%s: %v", tc.file, err)
			continue
		}
		if !strings.Contains(strings.Join(res.Issues, "\n"), tc.want) {
			t.Errorf("%s: no issue names %s in %s", tc.file, tc.want, strings.Join(res.Issues, "; "))
		}
		if res.Invoice.ID == "" {
			t.Errorf("%s: the invoice was lost while reporting the problem", tc.file)
		}
	}
}

// Input that is not an invoice document is an error, not an empty invoice: a
// caller that gets an Invoice back is entitled to use it.
func TestParseRefusesWhatIsNotAnInvoice(t *testing.T) {
	tbl := []struct {
		name string
		doc  string
	}{
		{"empty", ""},
		{"not xml", "invoice 2026-114 total 1625.00"},
		{"another schema", `<PurchaseOrder xmlns="urn:oasis:names:specification:ubl:schema:xsd:Order-2"><cbc:ID>1</cbc:ID></PurchaseOrder>`},
		{"unclosed", strings.Replace(string(read(t, "testdata/samples/ubl-min.xml")), "</cbc:ID>", "", 1)},
	}
	for _, tc := range tbl {
		res, err := einvoice.Parse([]byte(tc.doc))
		if err == nil {
			t.Errorf("%s: accepted, and returned syntax %q with id %q", tc.name, res.Syntax, res.Invoice.ID)
		}
	}
}

// The point of the reader is the round trip: a customer's CII document becomes
// a model, and the model becomes a UBL document that reads back as the same
// invoice.
func TestParseSurvivesARoundTrip(t *testing.T) {
	first, err := einvoice.Parse(read(t, "testdata/samples/cii-xrechnung.xml"))
	if err != nil {
		t.Fatal(err)
	}
	ubl, err := einvoice.GenerateUBL(first.Invoice)
	if err != nil {
		t.Fatal(err)
	}
	second, err := einvoice.Parse(ubl)
	if err != nil {
		t.Fatal(err)
	}
	if second.Syntax != einvoice.SyntaxUBL {
		t.Errorf("the document written as UBL read back as %q", second.Syntax)
	}
	a, b := first.Invoice, second.Invoice
	if a.ID != b.ID || a.Currency != b.Currency {
		t.Errorf("identity lost: %q/%q then %q/%q", a.ID, a.Currency, b.ID, b.Currency)
	}
	if len(a.Lines) != len(b.Lines) {
		t.Errorf("%d lines became %d", len(a.Lines), len(b.Lines))
	}
	pa, pb := a.Totals.Payable, b.Totals.Payable
	if pa == nil || pb == nil || *pa != *pb {
		t.Errorf("payable %v became %v", pa, pb)
	}
}

// A converted document has to carry its own rule set with it. The German rules
// run against UBL just as they do against CII, and a document that arrives at
// the other syntax missing an element or holding one in the wrong place fails
// at the receiving end, which is the last place anyone wants to find that out.
// Conversion may therefore not introduce a finding the source document did not
// already have, and may not hide one either.
func TestConversionIntroducesNoNewBreach(t *testing.T) {
	for _, tc := range []struct {
		file  string
		asCII bool
		opt   einvoice.Options
	}{
		{"testdata/samples/cii-xrechnung.xml", false, einvoice.Options{CIUS: einvoice.CIUSXRechnung}},
		{"testdata/samples/ubl-peppol.xml", true, einvoice.Options{Profile: einvoice.ProfilePeppol}},
	} {
		data := read(t, tc.file)
		first, err := einvoice.Parse(data)
		if err != nil {
			t.Fatal(err)
		}
		out, err := einvoice.GenerateCII(first.Invoice)
		if !tc.asCII {
			out, err = einvoice.GenerateUBL(first.Invoice)
		}
		if err != nil {
			t.Fatal(err)
		}
		before, err := einvoice.Validate(data, tc.opt)
		if err != nil {
			t.Fatal(err)
		}
		after, err := einvoice.Validate(out, tc.opt)
		if err != nil {
			t.Fatal(err)
		}
		if after.Internal > 0 {
			t.Fatalf("%s: the converted document could not be evaluated: %+v", tc.file, after)
		}
		had, have := map[string]bool{}, map[string]bool{}
		for _, f := range before.Findings {
			had[f.ID] = true
		}
		for _, f := range after.Findings {
			have[f.ID] = true
			if !had[f.ID] {
				t.Errorf("%s: conversion introduced %s at %s: %s", tc.file, f.ID, f.Context, f.Message)
			}
		}
		for _, f := range before.Findings {
			if !have[f.ID] {
				t.Errorf("%s: conversion lost %s: %s", tc.file, f.ID, f.Message)
			}
		}
	}
}

// The wasm layer hands ParseResult to JavaScript as JSON, where a nil slice is
// null and an empty one is []. A page should not have to test for two kinds of
// "nothing found", so a document that fits the model encodes an empty array.
func TestParseEncodesIssuesAsAnArray(t *testing.T) {
	res, err := einvoice.Parse(read(t, "testdata/samples/ubl-peppol.xml"))
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Issues) != 0 {
		t.Fatalf("Issues = %v, want none", res.Issues)
	}
	doc, err := json.Marshal(res)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(doc), `"issues":[]`) {
		t.Errorf("issues did not encode as an empty array: %.200s", doc)
	}
}
