package einvoice_test

import (
	"bytes"
	"strings"
	"testing"

	"github.com/dcnws/invoicekit.go"
)

// smallInvoice is a complete, ordinary invoice: both parties identified, one
// standard rated line, a document allowance and a charge, and totals left to be
// computed by the generator.
func smallInvoice() einvoice.Invoice {
	return einvoice.Invoice{
		ID:        "INV-2024-0142",
		IssueDate: "2024-03-14",
		DueDate:   "2024-04-13",
		TypeCode:  "380",
		Currency:  "EUR",
		Seller: einvoice.Seller{Party: einvoice.Party{
			Name:     "Hale and Sons Ltd",
			Address:  einvoice.Address{Line1: "12 Bunhill Row", City: "London", Postal: "EC1Y 8LP", Country: "GB"},
			TaxIDs:   []einvoice.TaxID{{Value: "GB123456789", Scheme: "VAT"}},
			Contact:  &einvoice.Contact{Name: "Accounts", Email: "accounts@hale.example"},
			EAddress: "electronic@hale.example",
		}},
		Buyer: einvoice.Party{
			Name:       "Contoso Retail Ltd",
			Address:    einvoice.Address{Line1: "45 Market Street", City: "Manchester", Postal: "M1 1AA", Country: "GB"},
			TaxIDs:     []einvoice.TaxID{{Value: "GB987654321", Scheme: "VAT"}},
			Identifier: "CUST-0091",
			IDScheme:   "0088",
		},
		Payment:       &einvoice.Payment{Code: "30", Name: "Credit transfer", RemitInfo: "REF INV-2024-0142"},
		SellerAccount: &einvoice.FinancialAcct{IBAN: "GB29HABB12345678901234", BIC: "HABBGB2L"},
		References:    []einvoice.Reference{{Type: "purchase", ID: "PO-5512"}},
		Terms:         []string{"Payment due within 30 days of invoice date."},
		Allowances:    []einvoice.AllowanceCharge{{Percent: new(5.0), Reason: "Volume discount", ReasonCode: "95", TaxCategory: "S", TaxRate: new(20.0)}},
		Charges:       []einvoice.AllowanceCharge{{Amount: 12.5, Reason: "Handling", ReasonCode: "AA", TaxCategory: "S", TaxRate: new(20.0)}},
		Lines: []einvoice.Line{{
			ID: "1", Quantity: 10, UnitCode: "C62", NetPrice: 24,
			TaxCategory: "S", TaxRate: 20,
			Item: einvoice.Item{Name: "Widget", SellerID: "W-100"},
		}},
	}
}

func TestGenerateBothSyntaxes(t *testing.T) {
	inv := smallInvoice()
	for _, sx := range []einvoice.Syntax{einvoice.SyntaxCII, einvoice.SyntaxUBL} {
		data, err := einvoice.Generate(sx, inv)
		if err != nil {
			t.Fatalf("Generate(%s): %v", sx, err)
		}
		if !bytes.Contains(data, []byte("<")) {
			t.Fatalf("Generate(%s): output is not XML: %q", sx, data)
		}
		if got, err := einvoice.Detect(data); err != nil {
			t.Fatalf("Detect after Generate(%s): %v", sx, err)
		} else if got != sx {
			t.Errorf("Generate(%s) was detected as %s", sx, got)
		}
		res, err := einvoice.Validate(data, einvoice.Options{})
		if err != nil {
			t.Fatalf("Validate after Generate(%s): %v", sx, err)
		}
		for _, f := range res.Findings {
			t.Logf("%s finding %s: %s", f.Flag, f.ID, f.Message)
		}
		if res.Fatal > 0 {
			t.Errorf("Generate(%s) broke %d business rules: %v", sx, res.Fatal, strings.Join(res.IDs(), " "))
		}
		if res.Internal > 0 {
			t.Errorf("Generate(%s) produced %d unevaluable rules", sx, res.Internal)
		}
	}
}

// The generated document has to carry the numbers the norm requires, computed
// from the lines rather than guessed by the caller.
func TestGenerateTotals(t *testing.T) {
	inv := smallInvoice() // 10 x 24 = 240, less 5% (12) plus 12.50 charge = 240.50
	data, err := einvoice.GenerateCII(inv)
	if err != nil {
		t.Fatal(err)
	}
	doc := string(data)
	// 10 x 24.00 = 240.00, plus a 12.50 charge, less 5% of 252.50 = 12.63,
	// giving a taxable base of 239.87 and 20% VAT of 47.97.
	for _, want := range []string{
		"<ram:LineTotalAmount>240.00</ram:LineTotalAmount>",
		"<ram:ChargeTotalAmount>12.50</ram:ChargeTotalAmount>",
		"<ram:AllowanceTotalAmount>12.63</ram:AllowanceTotalAmount>",
		"<ram:TaxBasisTotalAmount>239.87</ram:TaxBasisTotalAmount>",
		`<ram:TaxTotalAmount currencyID="EUR">47.97</ram:TaxTotalAmount>`,
		"<ram:GrandTotalAmount>287.84</ram:GrandTotalAmount>",
		"<ram:DuePayableAmount>287.84</ram:DuePayableAmount>",
	} {
		if !strings.Contains(doc, want) {
			t.Errorf("generated CII is missing %s", want)
		}
	}
}

func TestGenerateUnknownSyntax(t *testing.T) {
	if _, err := einvoice.Generate("PDF", smallInvoice()); err == nil {
		t.Fatal("Generate accepted an unknown syntax")
	}
}

func TestCheckModel(t *testing.T) {
	if probs := einvoice.Check(smallInvoice()); len(probs) > 0 {
		t.Fatalf("a complete invoice reported problems: %v", probs)
	}
	bad := smallInvoice()
	bad.ID = ""
	bad.Lines[0].Quantity = 0
	bad.Payment = nil
	probs := einvoice.Check(bad)
	if len(probs) < 3 {
		t.Fatalf("an incomplete invoice reported only %d problems: %v", len(probs), probs)
	}
	seen := map[string]bool{}
	for _, p := range probs {
		seen[p.Rule] = true
	}
	for _, rule := range []string{"BR-02", "BR-22", "BR-81"} {
		if !seen[rule] {
			t.Errorf("Check missed %s", rule)
		}
	}
}
