package einvoice_test

import (
	"bytes"
	"strings"
	"testing"

	"github.com/dcnws/invoicekit.go"
)

// TestDecodeInvoiceModelRefusesFieldsTheModelDoesNotHave is the reason a model
// is decoded strictly. A key the model has no room for is not a small
// formatting problem: it is a field the author believes is on the invoice and
// which will not be. "postName" is the example — EN 16931 has no P.O. box term
// at all, so a generator that accepted the key would either drop it or write an
// element the standard forbids, and the invoice would look fine on the way out.
func TestDecodeInvoiceModelRefusesFieldsTheModelDoesNotHave(t *testing.T) {
	ok := `{"id":"1","issueDate":"2026-01-05","typeCode":"380","currency":"GBP",
		"seller":{"name":"S","address":{"country":"GB"}},
		"buyer":{"name":"B","address":{"country":"DE"}},
		"lines":[{"id":"1","quantity":1,"netPrice":100,"taxCategory":"S","taxRate":20,"item":{"name":"Widget"}}]}`

	if _, err := einvoice.DecodeInvoiceModel(strings.NewReader(ok)); err != nil {
		t.Fatalf("a well formed model was refused: %v", err)
	}

	invented := strings.Replace(ok, `"country":"GB"`, `"country":"GB","postName":"PO Box 12"`, 1)
	_, err := einvoice.DecodeInvoiceModel(strings.NewReader(invented))
	if err == nil {
		t.Fatal("postName is not a field of the model and must not be accepted")
	}
	if !strings.Contains(err.Error(), "postName") {
		t.Errorf("the error has to name the field the caller invented, got: %v", err)
	}
}

// TestDecodeInvoiceModelRefusesMoreThanOneDocument: two JSON objects in one file
// decode as one invoice and a discarded remainder, which is how a concatenated
// batch ends up as a single invoice nobody notices.
func TestDecodeInvoiceModelRefusesMoreThanOneDocument(t *testing.T) {
	one := `{"id":"1","issueDate":"2026-01-05","typeCode":"380","currency":"GBP",
		"seller":{"name":"S","address":{"country":"GB"}},
		"buyer":{"name":"B","address":{"country":"DE"}},
		"lines":[{"id":"1","quantity":1,"netPrice":100,"taxCategory":"S","taxRate":20,"item":{"name":"Widget"}}]}`
	_, err := einvoice.DecodeInvoiceModel(strings.NewReader(one + "\n" + one))
	if err == nil {
		t.Fatal("the second invoice was swallowed without a word")
	}
	if !strings.Contains(err.Error(), "second") {
		t.Errorf("the error should say there is more than one document, got: %v", err)
	}
}

// TestNoCIIElementProhibitedByTheNormIsEverWritten. CII-DT-083 forbids
// ram:PostOfficeBox outright, so a generator must never reach for it, and the
// model has no field that could. The same guard runs over UBL, where the
// element does not exist in the profile at all.
func TestNoProhibitedCIIElementIsEverWritten(t *testing.T) {
	inv := exampleModel(t, "invoice-model.json")
	for _, sx := range []einvoice.Syntax{einvoice.SyntaxCII, einvoice.SyntaxUBL} {
		doc, err := einvoice.Generate(sx, inv)
		if err != nil {
			t.Fatalf("%s: %v", sx, err)
		}
		for _, banned := range []string{"PostOfficeBox"} {
			if bytes.Contains(doc, []byte(banned)) {
				t.Errorf("%s output writes %s, which EN 16931 does not have", sx, banned)
			}
		}
	}
}

// TestDecodeInvoiceModelNamesTheTermTheCallerWasLookingFor. A refusal that only
// refuses sends the author back to the specification, which is where they have
// already been. The keys in einvoice.modelHints are the ones people reach for
// when EN 16931 has a nearby term, so the error has to point at that term and
// say nothing false about it.
func TestDecodeInvoiceModelNamesTheTermTheCallerWasLookingFor(t *testing.T) {
	base := `{"id":"1","issueDate":"2026-01-05","typeCode":"380","currency":"GBP",
		"seller":{"name":"S","address":{"country":"GB"}},
		"buyer":{"name":"B","address":{"country":"DE"}},
		"lines":[{"id":"1","quantity":1,"netPrice":100,"taxCategory":"S","taxRate":20,"item":{"name":"Widget"}}]}`

	cases := []struct{ key, after, want string }{
		// EN 16931 has no payer party: the buyer is the payer.
		{"payer", `{`, "buyer"},
		// CII-DT-083 forbids the element, so the box number has to be a line.
		{"postName", `"address":{`, "address line"},
		// BG-15, not a shipping block of its own.
		{"shipTo", `"buyer":{`, "delivery"},
		// BT-84 and BT-91 hang off a party, not off the invoice.
		{"iban", `{`, "sellerAccount"},
	}
	for _, c := range cases {
		body := strings.Replace(base, c.after, c.after+`"`+c.key+`":"x",`, 1)
		if body == base {
			t.Fatalf("%s: the fixture was not modified, so nothing was tested", c.key)
		}
		_, err := einvoice.DecodeInvoiceModel(strings.NewReader(body))
		if err == nil {
			t.Fatalf("%s was accepted", c.key)
		}
		if !strings.Contains(err.Error(), c.key) {
			t.Errorf("%s: the error must name the key, got %v", c.key, err)
		}
		if !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: the error should point at %q, got %v", c.key, c.want, err)
		}
	}

	// A key the standard genuinely has nothing for gets the plain refusal,
	// rather than a confident sentence about a term that does not exist.
	_, err := einvoice.DecodeInvoiceModel(strings.NewReader(strings.Replace(base, `"currency":"GBP"`, `"currency":"GBP","carrier":"DHL"`, 1)))
	if err == nil {
		t.Fatal("carrier is not a field of the model")
	}
	if !strings.Contains(err.Error(), "no such field") {
		t.Errorf("an unhelpful key should get the plain refusal, got: %v", err)
	}
}
