package model

import (
	"fmt"
	"os"
	"slices"
	"sort"
	"strings"
	"testing"

	"github.com/dcnws/invoicekit.go/internal/en16931"
)

func fp(v float64) *float64 { return &v }

// base is the smallest invoice that is fully conformant: both parties carry a
// name, a complete postal address and a VAT identifier, a payment means is
// present and there is a single standard-rated line.
func base() Invoice {
	return Invoice{
		ID:        "INV-2024-0142",
		IssueDate: "2024-03-14",
		TypeCode:  "380",
		Currency:  "EUR",
		Seller: Seller{Party: Party{
			Name:     "Hale and Sons Ltd",
			Address:  Address{Line1: "12 Bunhill Row", City: "London", Zone: "Greater London", Postal: "EC1Y 8LP", Country: "GB"},
			TaxIDs:   []TaxID{{Value: "GB123456789", Scheme: "VAT"}},
			Contact:  &Contact{Name: "Accounts", Phone: "+44 20 7946 0000", Email: "accounts@hale.example"},
			EAddress: "electronic@hale.example",
		}, TradingName: "Hale Trading"},
		Buyer: Party{
			Name:       "Contoso Retail Ltd",
			Address:    Address{Line1: "45 Market Street", City: "Manchester", Zone: "Greater Manchester", Postal: "M1 1AA", Country: "GB"},
			TaxIDs:     []TaxID{{Value: "GB987654321", Scheme: "VAT"}},
			Identifier: "CUST-0091",
			IDScheme:   "0088",
		},
		Payment:       &Payment{Code: "30", Name: "Credit transfer", RemitInfo: "REF INV-2024-0142"},
		SellerAccount: &FinancialAcct{IBAN: "GB29HABB12345678901234", BIC: "HABBGB2L", Name: "Hale and Sons"},
		Lines: []Line{{
			ID: "1", Quantity: 10, UnitCode: "C62", NetPrice: 24,
			TaxCategory: "S", TaxRate: 20, Item: Item{Name: "Widget", SellerID: "W-100"},
		}},
	}
}

func full() Invoice {
	in := base()
	in.BuyerReference = "BA-2210"
	in.DueDate = "2024-04-13"
	in.ProcessType = "urn:fdc:peppol.eu:2017:poacc:billing:01:1.0"
	in.Notes = []string{"Goods dispatched by road.", "Thank you for your custom."}
	in.Accounting = &Account{ID: "LEDGER-4471", Type: "PR"}
	in.Payee = &Party{Name: "Hale Corporate Services Ltd",
		Address: Address{Line1: "12 Bunhill Row", City: "London", Postal: "EC1Y 8LP", Country: "GB"}}
	in.BuyerAccount = &FinancialAcct{IBAN: "DE89370400440532013000", Name: "Contoso Retail"}
	in.Payment.Mandate = &Mandate{ID: "MND-2024-7", Bank: in.BuyerAccount}
	in.Payment.Card = &Card{Pan: "4111111111", Holder: "H Hale", Type: "CASH", Scheme: "CARD"}
	in.Delivery = &Delivery{
		Name: "Warehouse 4", ReceiverID: "RCV-4",
		Address:   Address{Line1: "Quayside Depot", City: "Liverpool", Postal: "L3 4BQ", Country: "GB"},
		ShipTrade: "DAP", ShipPlace: "Warehouse 4", ArrivalDate: "2024-03-20", DepartureDate: "2024-03-18",
		Period: &Period{Start: "2024-03-18", End: "2024-03-20"},
	}
	in.References = []Reference{
		{Type: "contract", ID: "CTR-991"},
		{Type: "salesOrder", ID: "SO-2201"},
		{Type: "buyerOrder", ID: "BO-3310"},
		{Type: "quotation", ID: "QT-1180"},
		{Type: "preceding", ID: "INV-2024-0100"},
		{Type: "project", ID: "PRJ-7", Name: "Northside fit-out"},
		{Type: "despatch", ID: "DA-88"},
		{Type: "deliveryNote", ID: "DN-88"},
	}
	in.Period = &Period{Start: "2024-02-01", End: "2024-02-29", Text: "February 2024"}
	in.Terms = []string{"Payment due within 30 days of invoice date."}
	in.Allowances = []AllowanceCharge{{Percent: fp(5), Reason: "Volume discount", ReasonCode: "95", TaxCategory: "S", TaxRate: fp(20)}}
	in.Charges = []AllowanceCharge{{Amount: 12.5, Reason: "Handling", ReasonCode: "AA", TaxCategory: "S", TaxRate: fp(20)}}
	in.Prepaid = fp(100)
	in.Rounding = fp(0.01)
	in.Lines = []Line{
		{
			ID: "1", Note: "Includes fitting", Quantity: 10, UnitCode: "C62",
			GrossPrice: 30, NetPrice: 24, TaxCategory: "S", TaxRate: 20,
			Item: Item{Name: "Widget", Description: "Large blue widget", SellerID: "W-100", CustomerID: "C-9",
				StandardID: "4000000000001", StdScheme: "0088", ClassIDs: []string{"62149502"}, ClassScheme: "ST",
				Origin: "GB", Units: "C62",
				Price:      &ItemPrice{BaseQty: 1, BaseUnit: "C62", Allowance: fp(6)},
				Attributes: []Attribute{{Name: "Colour", Value: "Blue"}}},
			Allowances: []AllowanceCharge{{Percent: fp(10), Reason: "Loyalty", ReasonCode: "95", TaxCategory: "S", TaxRate: fp(20)}},
			Period:     &Period{Start: "2024-02-01", End: "2024-02-15"},
			Account:    &Account{ID: "CC-100"},
		},
		{
			ID: "2", Quantity: 3, UnitCode: "C62", NetPrice: 120, TaxCategory: "S", TaxRate: 20,
			Item:    Item{Name: "Gadget", Attributes: []Attribute{{Name: "Weight", Value: "2 kg"}}},
			Charges: []AllowanceCharge{{Amount: 5, Reason: "Express build", ReasonCode: "AA", TaxCategory: "S", TaxRate: fp(20)}},
		},
		{
			ID: "3", Quantity: 1, UnitCode: "C62", NetPrice: 40, TaxCategory: "Z", TaxRate: 0,
			Item: Item{Name: "Zero rated spare"},
		},
	}
	return in
}

func cases() map[string]Invoice {
	cr := base()
	cr.TypeCode = "381"
	ex := base()
	ex.Lines[0].TaxCategory = "E"
	ex.Lines[0].TaxRate = 0
	ex.Lines[0].TaxExempt = &TaxExempt{Reason: "Export outside the EU", ReasonCode: "VATEX-EU-O"}
	ae := base()
	ae.Lines[0].TaxCategory = "AE"
	ae.Lines[0].TaxRate = 0
	ae.Lines[0].TaxExempt = &TaxExempt{Reason: "Reverse charge", ReasonCode: "VATEX-EU-AE"}
	ae.Buyer.TaxIDs = nil
	ae.Buyer.RegIDs = []string{"987654321"}
	// pb carries a third address line on all three parties, which is the only
	// way a UBL cac:AddressLine is written at all.
	pb := base()
	pb.Seller.Address.Line3 = "PO Box 14"
	pb.Buyer.Address.Line3 = "C/O Purchasing"
	pb.Delivery = &Delivery{Name: "Warehouse 4", Address: Address{
		Line1: "Quayside Depot", Line3: "Gate 2", City: "Liverpool", Postal: "L3 4BQ", Country: "GB"}}
	return map[string]Invoice{"simple": base(), "full": full(), "credit": cr, "exempt": ex, "ae": ae, "postbox": pb}
}

// ciiAdvisories pins the advisory rules each rendered CII document attracts.
// The simple, credit, exempt and ae cases stay inside EN 16931 and so attract
// nothing; full goes beyond it on purpose, testing every field the model has,
// and pays nine warnings for it.
var ciiAdvisories = map[string][]string{
	"full": {
		"CII-SR-293", // incoterms beside the delivery
		"CII-SR-296", // quotation reference
		"CII-SR-319", // despatch date
		"CII-SR-329", // period around the delivery
		"CII-SR-335", // delivery note reference
		"CII-SR-360", // postal address on the payee
		"CII-SR-373", // card type code
		"CII-SR-382", // name on the paying account
		"CII-SR-429", // type on the buyer's accounting account
	},
}

func TestRenderCII(t *testing.T) {
	for name, inv := range cases() {
		t.Run(name, func(t *testing.T) {
			before := inv.ID
			data, err := RenderCII(inv)
			if err != nil {
				t.Fatalf("render: %v", err)
			}
			if inv.ID != before {
				t.Errorf("render mutated the caller's invoice")
			}
			rep, err := en16931.Validate(data, en16931.Options{})
			if err != nil {
				t.Fatalf("validate: %v", err)
			}

			// Schematron answers business questions and assumes a structurally
			// valid tree, so schema validity is asserted separately: a renderer
			// that puts an element where its schema does not allow it can pass
			// every business rule and still be unreadable by a real receiver.
			for _, f := range rep.Schema {
				t.Errorf("schema: %s", f)
			}
			// The CII schematron also carries advisory rules - "element X
			// should not be present" - flagged as warnings. They are guidance
			// about how CII documents are usually written, not part of the
			// EN 16931 conformance bar: the official validator reports them
			// and passes the document anyway. Every one of them fires on data
			// that EN 16931 does not define and CII has an element for, which
			// is why the renderer keeps writing that data rather than losing
			// it (see the note on RenderCII). The set is still pinned per
			// case below, so taking on a new advisory is a decision made in
			// this test rather than a surprise in a customer's log.
			n, ids := 0, []string{}
			seen := []string{}
			for _, f := range rep.Findings {
				if f.Flag == "warning" {
					t.Logf("advisory: %s %s", f.ID, f.Message)
					seen = append(seen, f.ID)
					continue
				}
				n++
				ids = append(ids, f.ID)
			}
			sort.Strings(seen)
			if want := ciiAdvisories[name]; !slices.Equal(seen, want) {
				t.Errorf("advisories = %v, want %v", seen, want)
			}
			if n == 0 {
				return
			}
			path := fmt.Sprintf("/tmp/cii-%s.xml", name)
			if err := os.WriteFile(path, data, 0o644); err != nil {
				t.Error(err)
			}
			t.Errorf("%d findings, document at %s, ids=%v", n, path, ids)
			for _, f := range rep.Findings {
				if f.Flag == "warning" {
					continue
				}
				t.Errorf("  %s [%s] %s ctx=%s err=%v", f.ID, f.Flag, f.Message, f.Context, f.Err)
			}
		})
	}
}

// Both syntaxes have fewer reference slots than EN 16931 has reference types:
// CII has one buyer order element, and a UBL order reference has two ids. A
// model that cannot be represented has to come back as an error rather than as
// a document that quietly loses a reference.
func TestRenderReferenceCollisions(t *testing.T) {
	dupe := base()
	dupe.References = []Reference{
		{Type: "buyerOrder", ID: "BO-3310"},
		{Type: "purchase", ID: "PO-5512"},
	}
	if _, err := RenderCII(dupe); err == nil {
		t.Error("CII rendered two buyer order references")
	} else {
		t.Logf("CII: %v", err)
	}

	three := base()
	three.References = []Reference{
		{Type: "buyerOrder", ID: "BO-3310"},
		{Type: "purchase", ID: "PO-5512"},
		{Type: "salesOrder", ID: "SO-2201"},
	}
	if _, err := RenderUBL(three); err == nil {
		t.Error("UBL rendered three order references")
	} else {
		t.Logf("UBL: %v", err)
	}

	unknown := base()
	unknown.References = []Reference{{Type: "waybill", ID: "WB-1"}}
	if _, err := RenderCII(unknown); err == nil {
		t.Error("CII accepted a reference type it has no element for")
	}
	if _, err := RenderUBL(unknown); err == nil {
		t.Error("UBL accepted a reference type it has no element for")
	}
}

// TestRenderUBL runs the same five documents through the UBL renderer and the
// UBL schematron. As in the CII test only the fatal findings have to be clear;
// the advisory UBL-CR rules are pinned the same way.
// ublAdvisories is the UBL side of the same pin. The two rule sets are not
// symmetrical: the same extra data is ordinary in one syntax and advisory in
// the other, so this list is not a copy of ciiAdvisories.
var ublAdvisories = map[string][]string{}

func TestRenderUBL(t *testing.T) {
	for name, inv := range cases() {
		t.Run(name, func(t *testing.T) {
			before := inv.ID
			data, err := RenderUBL(inv)
			if err != nil {
				t.Fatalf("render: %v", err)
			}
			if inv.ID != before {
				t.Errorf("render mutated the caller's invoice")
			}
			rep, err := en16931.Validate(data, en16931.Options{})
			if err != nil {
				t.Fatalf("validate: %v", err)
			}

			// Schematron answers business questions and assumes a structurally
			// valid tree, so schema validity is asserted separately: a renderer
			// that puts an element where its schema does not allow it can pass
			// every business rule and still be unreadable by a real receiver.
			for _, f := range rep.Schema {
				t.Errorf("schema: %s", f)
			}
			n, ids := 0, []string{}
			seen := []string{}
			for _, f := range rep.Findings {
				if f.Flag == "warning" {
					t.Logf("advisory: %s %s", f.ID, f.Message)
					seen = append(seen, f.ID)
					continue
				}
				n++
				ids = append(ids, f.ID)
			}
			sort.Strings(seen)
			if want := ublAdvisories[name]; !slices.Equal(seen, want) {
				t.Errorf("advisories = %v, want %v", seen, want)
			}
			if n == 0 {
				return
			}
			path := fmt.Sprintf("/tmp/ubl-%s.xml", name)
			if err := os.WriteFile(path, data, 0o644); err != nil {
				t.Error(err)
			}
			t.Errorf("%d findings, document at %s, ids=%v", n, path, ids)
			for _, f := range rep.Findings {
				if f.Flag == "warning" {
					continue
				}
				t.Errorf("  %s [%s] %s ctx=%s err=%v", f.ID, f.Flag, f.Message, f.Context, f.Err)
			}
		})
	}
}

func TestCIIArithmetic(t *testing.T) {
	in := full()
	if err := in.Derive(); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name string
		have *float64
		want float64
	}{
		{"lineTotal", in.Totals.LineTotal, 621.0},
		{"chargeTotal", in.Totals.ChargeTotal, 12.5},
		{"allowanceTotal", in.Totals.AllowanceTotal, 31.68},
		{"taxExclusive", in.Totals.TaxExclusive, 601.82},
		{"taxTotal", in.Totals.TaxTotal, 112.36},
		{"taxInclusive", in.Totals.TaxInclusive, 714.18},
		{"payable", in.Totals.Payable, 614.19},
	} {
		if tc.have == nil {
			t.Errorf("%s: nil", tc.name)
			continue
		}
		if Money(*tc.have) != Money(tc.want) {
			t.Errorf("%s = %s, want %s", tc.name, Money(*tc.have), Money(tc.want))
		}
	}
}

// span returns the first run of s between openTag and closeTag, inclusive.
func span(s, openTag, closeTag string) string {
	i := strings.Index(s, openTag)
	if i < 0 {
		return ""
	}
	j := strings.Index(s[i:], closeTag)
	if j < 0 {
		return ""
	}
	return s[i : i+j+len(closeTag)]
}

// TestRenderPriceSlots pins where a price discount goes in each syntax. The
// schematron does not check the placement (it only checks that a gross price is
// not negative), so a document with the discount in the wrong element passes
// validation here and fails at a schema aware consumer, which is exactly the
// class of bug that is expensive to find later.
func TestRenderPriceSlots(t *testing.T) {
	cii, err := RenderCII(full())
	if err != nil {
		t.Fatalf("cii: %v", err)
	}
	gross := span(string(cii), "<ram:GrossPriceProductTradePrice>", "</ram:GrossPriceProductTradePrice>")
	if gross == "" {
		t.Fatal("no gross price block in the generated CII")
	}
	for _, want := range []string{
		"<ram:ChargeAmount>30.00</ram:ChargeAmount>",
		"<ram:AppliedTradeAllowanceCharge>",
		"<udt:Indicator>false</udt:Indicator>",
		"<ram:ActualAmount>6.00</ram:ActualAmount>",
	} {
		if !strings.Contains(gross, want) {
			t.Errorf("CII gross price is missing %s:\n%s", want, gross)
		}
	}
	// The document level allowance element is not allowed inside a price, and
	// CII-SR-119 to CII-SR-130 want the applied one to carry the flag and the
	// amount only.
	for _, ban := range []string{
		"ram:SpecifiedTradeAllowanceCharge", "ram:CategoryTradeTax",
		"ram:CalculationPercent", "ram:BasisAmount", "ram:ReasonCode", "ram:Reason>",
	} {
		if strings.Contains(gross, ban) {
			t.Errorf("CII gross price carries %s:\n%s", ban, gross)
		}
	}
	net := span(string(cii), "<ram:NetPriceProductTradePrice>", "</ram:NetPriceProductTradePrice>")
	if !strings.Contains(net, "<ram:ChargeAmount>24.00</ram:ChargeAmount>") {
		t.Errorf("CII net price:\n%s", net)
	}

	ubl, err := RenderUBL(full())
	if err != nil {
		t.Fatalf("ubl: %v", err)
	}
	price := span(string(ubl), "<cac:Price>", "</cac:Price>")
	if price == "" {
		t.Fatal("no price block in the generated UBL")
	}
	for _, want := range []string{
		`<cbc:PriceAmount currencyID="EUR">24.00</cbc:PriceAmount>`,
		`<cbc:Amount currencyID="EUR">6.00</cbc:Amount>`,
		`<cbc:BaseAmount currencyID="EUR">30.00</cbc:BaseAmount>`,
	} {
		if !strings.Contains(price, want) {
			t.Errorf("UBL price is missing %s:\n%s", want, price)
		}
	}
	for _, ban := range []string{"cac:TaxCategory", "cbc:MultiplierFactorNumeric", "cbc:AllowanceChargeReason"} {
		if strings.Contains(price, ban) {
			t.Errorf("UBL price carries %s:\n%s", ban, price)
		}
	}
	// The schema orders the amount before the amount it was taken from.
	if strings.Index(price, "<cbc:Amount") > strings.Index(price, "<cbc:BaseAmount") {
		t.Errorf("UBL price puts BaseAmount before Amount:\n%s", price)
	}
}

// TestRenderPriceDerived checks that a discount on its own still produces a
// gross price, and that figures which do not add up are refused.
func TestRenderPriceDerived(t *testing.T) {
	inv := full()
	inv.Lines[0].GrossPrice = 0
	data, err := RenderCII(inv)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	gross := span(string(data), "<ram:GrossPriceProductTradePrice>", "</ram:GrossPriceProductTradePrice>")
	if !strings.Contains(gross, "<ram:ChargeAmount>30.00</ram:ChargeAmount>") {
		t.Errorf("a discount with no gross price did not derive one:\n%s", gross)
	}

	bad := full()
	bad.Lines[0].Item.Price.Allowance = fp(10)
	for name, render := range map[string]func(Invoice) ([]byte, error){"cii": RenderCII, "ubl": RenderUBL} {
		if _, err := render(bad); err == nil {
			t.Errorf("%s: a price that does not add up rendered without an error", name)
		} else if !strings.Contains(err.Error(), "gross price") {
			t.Errorf("%s: unexpected error: %v", name, err)
		}
	}
}

// TestRenderNoAdvisoryOnTaxBlocks covers the three advisory rules that the
// schematron reports as warnings rather than failures, so they are asserted by
// id: a line level allowance takes the VAT of its line, and a payment mandate
// account has no name.
func TestRenderNoAdvisoryOnTaxBlocks(t *testing.T) {
	banned := map[string][]string{
		"CII": {"CII-SR-191", "CII-SR-130"},
		"UBL": {"UBL-CR-558", "UBL-CR-641", "UBL-CR-635", "UBL-CR-440"},
	}
	for _, tc := range []struct {
		syntax string
		render func(Invoice) ([]byte, error)
	}{
		{"CII", RenderCII},
		{"UBL", RenderUBL},
	} {
		for name, inv := range cases() {
			t.Run(tc.syntax+"/"+name, func(t *testing.T) {
				data, err := tc.render(inv)
				if err != nil {
					t.Fatalf("render: %v", err)
				}
				rep, err := en16931.Validate(data, en16931.Options{})
				if err != nil {
					t.Fatalf("validate: %v", err)
				}
				for _, f := range rep.Findings {
					for _, id := range banned[tc.syntax] {
						if f.ID == id {
							t.Errorf("%s: %s ctx=%s", f.ID, f.Message, f.Context)
						}
					}
				}
			})
		}
	}
}

// TestRenderReferenceOrder pins the CII and UBL element order for order,
// quotation and contract references. Both schemas fix the sequence of these
// elements, so a caller who lists the references in any old order must still
// get a document a schema-aware consumer accepts. Listing them in the order
// they were handed over is the tempting implementation and the wrong one.
func TestRenderReferenceOrder(t *testing.T) {
	in := base()
	in.References = []Reference{
		{Type: "contract", ID: "CT-9"},
		{Type: "project", ID: "PJ-9", Name: "Riverside"},
		{Type: "buyerOrder", ID: "BO-3310"},
		{Type: "quotation", ID: "QT-4"},
		{Type: "salesOrder", ID: "SO-2201"},
	}
	for name, render := range map[string]func(Invoice) ([]byte, error){
		"CII": RenderCII,
		"UBL": RenderUBL,
	} {
		data, err := render(in)
		if err != nil {
			t.Fatalf("%s: render: %v", name, err)
		}
		rep, err := en16931.Validate(data, en16931.Options{})
		if err != nil {
			t.Fatalf("%s: validate: %v", name, err)
		}
		for _, f := range rep.Schema {
			t.Errorf("%s: %s: %s", name, f.Path, f.Message)
		}
	}
}
