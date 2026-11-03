package model

import (
	"encoding/json"
	"fmt"
	"math/big"
	"os"
	"slices"
	"strings"
	"testing"
)

// The reader is the renderer's mirror, so these tests are round trips: render a
// model, read it back, compare. Everything a syntax cannot say about an invoice
// shows up as a difference between the two models, so expectFor spells out what
// each writer drops and what it fills in. A new entry there is a decision about
// the reader, taken on purpose rather than discovered by accident.

// renders and reads map a syntax to its codec, which keeps the round trip
// tests one loop instead of a switch per syntax.
var renders = map[Syntax]func(Invoice) ([]byte, error){
	SyntaxCII: RenderCII,
	SyntaxUBL: RenderUBL,
}

var reads = map[Syntax]func([]byte) (*ReadReport, error){
	SyntaxCII: ReadCII,
	SyntaxUBL: ReadUBL,
}

var syntaxes = []Syntax{SyntaxCII, SyntaxUBL}

func renderFor(t *testing.T, sx Syntax, inv Invoice) []byte {
	t.Helper()
	data, err := renders[sx](inv)
	if err != nil {
		t.Fatalf("render %s: %v", sx, err)
	}
	return data
}

func readFor(t *testing.T, sx Syntax, data []byte) *ReadReport {
	t.Helper()
	rep, err := reads[sx](data)
	if err != nil {
		t.Fatalf("read %s: %v", sx, err)
	}
	return rep
}

func fixture(t *testing.T, name string) Invoice {
	t.Helper()
	b, err := os.ReadFile("../../testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	var inv Invoice
	if err := json.Unmarshal(b, &inv); err != nil {
		t.Fatal(err)
	}
	return inv
}

func sample(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile("../../testdata/samples/" + name)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// copyInvoice deep copies an invoice. The writers share pointers into their
// argument, and expectFor edits the copy, so the original has to stay intact.
func copyInvoice(t *testing.T, inv Invoice) Invoice {
	t.Helper()
	b, err := json.Marshal(inv)
	if err != nil {
		t.Fatal(err)
	}
	var out Invoice
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatal(err)
	}
	return out
}

// modelDiff compares two invoices and names the first line on which they
// differ, which is far more use than a wall of JSON.
func modelDiff(want, got Invoice) string {
	// References are a set: each syntax writes them in the order its schema
	// prescribes, so the reader returns them in that order and not in the
	// model's. Sorting both sides keeps the comparison honest without hiding
	// anything but position.
	a, b := copyInvoiceNoT(want), copyInvoiceNoT(got)
	slices.SortFunc(a.References, func(x, y Reference) int { return strings.Compare(x.Type+y.ID, y.Type+x.ID) })
	slices.SortFunc(b.References, func(x, y Reference) int { return strings.Compare(x.Type+y.ID, y.Type+x.ID) })

	ja, err1 := json.MarshalIndent(a, "", "  ")
	jb, err2 := json.MarshalIndent(b, "", "  ")
	if err1 != nil || err2 != nil {
		return fmt.Sprintf("marshal: %v %v", err1, err2)
	}
	if string(ja) == string(jb) {
		return ""
	}
	al, bl := strings.Split(string(ja), "\n"), strings.Split(string(jb), "\n")
	for i := 0; i < len(al) || i < len(bl); i++ {
		x, y := "<missing>", "<missing>"
		if i < len(al) {
			x = al[i]
		}
		if i < len(bl) {
			y = bl[i]
		}
		if x != y {
			return fmt.Sprintf("models differ at line %d:\n  want %s\n  got  %s", i+1, strings.TrimSpace(x), strings.TrimSpace(y))
		}
	}
	return "models differ"
}

func copyInvoiceNoT(inv Invoice) Invoice {
	b, _ := json.Marshal(inv)
	var out Invoice
	_ = json.Unmarshal(b, &out)
	return out
}

// expectFor is the model a round trip should produce: the invoice as the writer
// saw it, with every field that has no element of its own removed and every
// default the writer injects put back. The renderer always derives first, so
// the starting point is a derived copy.
func expectFor(t *testing.T, inv Invoice, sx Syntax) Invoice {
	t.Helper()
	out := copyInvoice(t, inv)
	if err := out.Derive(); err != nil {
		t.Fatalf("derive: %v", err)
	}
	if out.Guideline == "" {
		// Both syntaxes default the specification identifier to the same id.
		out.Guideline = CIIProfile
	}
	foldParty(&out.Seller.Party)
	foldParty(&out.Buyer)
	if out.Period != nil {
		out.Period.Text = "" // no element for a period narrative in either syntax
	}
	if out.Delivery != nil && out.Delivery.Period != nil {
		out.Delivery.Period.Text = ""
	}
	for i := range out.Lines {
		foldLine(&out.Lines[i])
	}
	// A percentage with no amount is written as both, and with no base as both
	// plus the base its position implies, so a round trip comes back holding
	// figures the caller did not type. That is the document saying what it
	// means, not the reader inventing money.
	docCharge, _ := lineTotalBasis(&out).Float64()
	docAllow := docCharge
	if out.Totals.ChargeTotal != nil {
		docAllow += *out.Totals.ChargeTotal
	}
	foldAC(out.Charges, ratOf(docCharge))
	foldAC(out.Allowances, ratOf(docAllow))
	for i := range out.Lines {
		ln := &out.Lines[i]
		// A line allowance or charge takes the VAT of its line, so neither
		// syntax writes a tax block on it.
		for j := range ln.Allowances {
			ln.Allowances[j].TaxCategory, ln.Allowances[j].TaxRate = "", nil
		}
		for j := range ln.Charges {
			ln.Charges[j].TaxCategory, ln.Charges[j].TaxRate = "", nil
		}
		foldAC(ln.Allowances, lineBase(*ln))
		foldAC(ln.Charges, lineBase(*ln))
	}
	if out.Payment != nil && out.Payment.Code == "" && out.Payment.Name == "" &&
		out.Payment.RemitInfo == "" && out.Payment.Card == nil && out.Payment.Mandate == nil {
		out.Payment = nil
	}
	switch sx {
	case SyntaxCII:
		// One element carries the payer's account, so a reader that finds a
		// mandate cannot tell whose account it was.
		if out.Payment != nil && out.Payment.Mandate != nil && out.Payment.Mandate.Bank != nil {
			out.BuyerAccount = nil
		}
		// CII has no card scheme and no reference name except for a project.
		if out.Payment != nil && out.Payment.Card != nil {
			out.Payment.Card.Scheme = ""
		}
		for i := range out.References {
			if out.References[i].Type != "project" {
				out.References[i].Name = ""
			}
		}
	case SyntaxUBL:
		if out.Payee != nil {
			out.Payee = &Party{Name: out.Payee.Name}
		}
		if out.Payment != nil {
			// A payer account has no UBL home outside a mandate.
			out.BuyerAccount = nil
			if c := out.Payment.Card; c != nil {
				c.Type = ""
				if c.Scheme == "" {
					c.Scheme = "CARD" // cbc:NetworkID is mandatory
				}
			}
			if m := out.Payment.Mandate; m != nil && m.Bank != nil {
				m.Bank.Name = ""
			}
			// UBL gives an account one identifier, and the reader sorts it by
			// shape, so a redacted or private number comes home as the other
			// identifier rather than as an IBAN.
			foldAcct(out.SellerAccount)
			if out.Payment != nil && out.Payment.Mandate != nil {
				foldAcct(out.Payment.Mandate.Bank)
			}
			if d := out.Delivery; d != nil {
				d.ShipTrade, d.ShipPlace, d.DepartureDate = "", "", ""
			}
		}
		if a := out.Accounting; a != nil {
			a.Type = ""
			if a.ID == "" {
				out.Accounting = nil
			}
		}
		for i := range out.References {
			out.References[i].Name = ""
		}
		for i := range out.Lines {
			out.Lines[i].TaxExempt = nil
			if a := out.Lines[i].Account; a != nil {
				a.Type = ""
			}
		}
	}
	return out
}

// foldAC fills in what a writer resolves for a list of allowances or charges:
// the amount a percentage stands for, and the base it was measured against.
func foldAC(list []AllowanceCharge, basis *big.Rat) {
	for i := range list {
		a := &list[i]
		if a.Amount == 0 && a.Percent != nil {
			f, _ := allowanceAmount(*a, basis).Float64()
			a.Amount = f
		}
		if a.Basis == nil && a.Percent != nil {
			f, _ := basis.Float64()
			a.Basis = &f
		}
		// An exempt allowance or charge carries no rate: both syntaxes write
		// the category and leave the percent out, so a round trip cannot
		// carry a zero that Derive put there.
		if a.TaxCategory == "O" {
			a.TaxRate = nil
		}
	}
}

// foldAcct applies the single identifier UBL gives a bank account: the writer
// prefers the IBAN, and the reader sorts whatever it reads by shape.
func foldAcct(a *FinancialAcct) {
	if a == nil {
		return
	}
	id := a.IBAN
	if id == "" {
		id = a.OtherID
	}
	if id == "" {
		return
	}
	if isIBAN(id) {
		a.IBAN, a.OtherID = id, ""
		return
	}
	a.IBAN, a.OtherID = "", id
}

// foldParty applies the party fields both syntaxes treat the same way.
func foldParty(p *Party) {
	if p.EAddress != "" {
		p.EAScheme = p.EASCode()
	} else {
		// The scheme labels an electronic address, and a document with no
		// address has nowhere to put a label: the CII sample that redacts the
		// buyer's address leaves an empty URIID holding one.
		p.EAScheme = ""
	}
	// One element carries the subdivision, and Zone is read back from it.
	if p.Address.Zone == "" {
		p.Address.Zone = p.Subdiv
	}
	p.Subdiv = ""
}

// foldLine applies the line fields both syntaxes treat the same way.
func foldLine(ln *Line) {
	ln.Unit = ""
	if ln.Item.Units != "" && ln.UnitCode == ln.Item.Units {
		ln.Item.Units = "" // the fallback is folded into the line's unit code
	}
	// Both syntaxes write the item's attributes and the line's attributes into
	// one list of item attributes, the reader cannot tell them apart.
	if len(ln.Attributes) > 0 {
		ln.Item.Attributes = append(slices.Clone(ln.Item.Attributes), ln.Attributes...)
		ln.Attributes = nil
	}
	// The price details group survives only as what the element actually says:
	// a basis quantity other than one unit, and the discount the document
	// carries. Both are measured the way the writer measures them.
	q, u := 1.0, ln.UnitCode
	p := ln.Item.Price
	if p != nil {
		if p.BaseQty != 0 {
			q = p.BaseQty
		}
		if p.BaseUnit != "" {
			u = p.BaseUnit
		}
	}
	net, gross, disc, bad := priceParts(*ln)
	if bad != "" {
		panic("test invoice: " + bad)
	}
	// The gross price is only in the document when it differs from the net, so
	// that is what a reader can claim back.
	if gross != net || disc != 0 {
		ln.GrossPrice = gross
	} else {
		ln.GrossPrice = 0
	}
	var keep *ItemPrice
	if q != 1 || u != ln.UnitCode || disc != 0 {
		keep = &ItemPrice{}
		if q != 1 || u != ln.UnitCode {
			keep.BaseQty, keep.BaseUnit = q, u
		}
		if disc != 0 {
			d := disc
			keep.Allowance = &d
		}
	}
	ln.Item.Price = keep
}

func TestReadRoundTripsModelFixtures(t *testing.T) {
	for _, name := range []string{"invoice-model.json", "xrechnung-model.json", "peppol-model.json"} {
		inv := fixture(t, name)
		for _, sx := range syntaxes {
			rep := readFor(t, sx, renderFor(t, sx, inv))
			if len(rep.Issues) > 0 {
				t.Errorf("%s as %s: issues: %s", name, sx, strings.Join(rep.Issues, "; "))
			}
			if d := modelDiff(expectFor(t, inv, sx), rep.Invoice); d != "" {
				t.Errorf("%s as %s: %s", name, sx, d)
			}
		}
	}
}

func TestReadRoundTripsRenderedCases(t *testing.T) {
	for name, inv := range cases() {
		for _, sx := range syntaxes {
			rep := readFor(t, sx, renderFor(t, sx, inv))
			if d := modelDiff(expectFor(t, inv, sx), rep.Invoice); d != "" {
				t.Errorf("%s as %s: %s", name, sx, d)
			}
		}
	}
}

func TestReadReportsWhatItCannotMap(t *testing.T) {
	tbl := []struct {
		sx     Syntax
		before string
		inject string
		want   []string
	}{
		{
			sx:     SyntaxCII,
			before: "<ram:ApplicableHeaderTradeSettlement>",
			inject: "<ram:SellerTaxRepresentativeTradeParty><ram:Name>Tax Rep Ltd</ram:Name>" +
				"<ram:PostalTradeAddress><ram:CountryID>GB</ram:CountryID></ram:PostalTradeAddress>" +
				"</ram:SellerTaxRepresentativeTradeParty>" +
				"<ram:AdditionalReferencedDocument><ram:IssuerAssignedID>ATT-1</ram:IssuerAssignedID></ram:AdditionalReferencedDocument>",
			want: []string{"ram:SellerTaxRepresentativeTradeParty", "ram:AdditionalReferencedDocument"},
		},
		{
			sx:     SyntaxUBL,
			before: "<cac:AccountingSupplierParty>",
			inject: "<cac:Signature><cbc:SignatureID>SIG-1</cbc:SignatureID></cac:Signature>" +
				"<cac:WithholdingTaxTotal><cbc:TaxAmount currencyID=\"EUR\">1.00</cbc:TaxAmount></cac:WithholdingTaxTotal>",
			want: []string{"cac:Signature", "cac:WithholdingTaxTotal"},
		},
	}
	for _, tc := range tbl {
		doc := string(renderFor(t, tc.sx, base()))
		i := strings.Index(doc, tc.before)
		if i < 0 {
			t.Fatalf("%s: no %s in the rendered document", tc.sx, tc.before)
		}
		doc = doc[:i] + tc.inject + doc[i:]
		rep := readFor(t, tc.sx, []byte(doc))
		if len(rep.Issues) != len(tc.want) {
			t.Errorf("%s: issues = %s, want %d", tc.sx, strings.Join(rep.Issues, "; "), len(tc.want))
			continue
		}
		for _, w := range tc.want {
			if !slices.ContainsFunc(rep.Issues, func(s string) bool { return strings.Contains(s, w) }) {
				t.Errorf("%s: no issue names %s in %s", tc.sx, w, strings.Join(rep.Issues, "; "))
			}
		}
		if rep.Invoice.ID != base().ID {
			t.Errorf("%s: the document was not read: %+v", tc.sx, rep.Invoice)
		}
	}
}

func TestReadRejectsAWrongRoot(t *testing.T) {
	cii := renderFor(t, SyntaxCII, base())
	ubl := renderFor(t, SyntaxUBL, base())
	if _, err := ReadCII(ubl); err == nil || !strings.Contains(err.Error(), "CII") {
		t.Errorf("ReadCII on UBL: %v, want an error saying the document is not CII", err)
	}
	if _, err := ReadUBL(cii); err == nil || !strings.Contains(err.Error(), "UBL") {
		t.Errorf("ReadUBL on CII: %v, want an error saying the document is not UBL", err)
	}
}

func TestReadBadNumberNamesTheElement(t *testing.T) {
	doc := strings.Replace(string(renderFor(t, SyntaxCII, base())),
		"<ram:LineTotalAmount>240.00", "<ram:LineTotalAmount>240,00", 1)
	_, err := ReadCII([]byte(doc))
	if err == nil {
		t.Fatal("a comma decimal was accepted")
	}
	if !strings.Contains(err.Error(), "ram:LineTotalAmount") {
		t.Errorf("error does not name the element: %v", err)
	}
}

func TestReadRejectsMalformedXML(t *testing.T) {
	doc := strings.Replace(string(renderFor(t, SyntaxUBL, base())),
		"<cac:AccountingSupplierParty>", "<cac:AccountingSupplierParty", 1)
	if _, err := ReadUBL([]byte(doc)); err == nil {
		t.Fatal("an unclosed element was accepted")
	}
}

func TestReadReturnsWhatTheDocumentSays(t *testing.T) {
	tbl := []struct {
		sx          Syntax
		open, close string
	}{
		{SyntaxCII, "<ram:DuePayableAmount>", "</ram:DuePayableAmount>"},
		{SyntaxUBL, `<cbc:PayableAmount currencyID="EUR">`, "</cbc:PayableAmount>"},
	}
	for _, tc := range tbl {
		doc := string(renderFor(t, tc.sx, base()))
		i, j := strings.Index(doc, tc.open), strings.Index(doc, tc.close)
		if i < 0 || j < 0 {
			t.Fatalf("%s: no payable amount in the document", tc.sx)
		}
		doc = doc[:i+len(tc.open)] + "1.00" + doc[j:]
		rep := readFor(t, tc.sx, []byte(doc))
		if p := rep.Invoice.Totals.Payable; p == nil || *p != 1 {
			t.Errorf("%s: payable = %v, want the 1.00 the document says", tc.sx, p)
		}
		if len(rep.Issues) > 0 {
			t.Errorf("%s: issues: %s", tc.sx, strings.Join(rep.Issues, "; "))
		}
	}
}

func TestReadOfficialSamples(t *testing.T) {
	tbl := []struct {
		file      string
		sx        Syntax
		id        string
		currency  string
		payable   float64
		lines     int
		seller    string
		buyerRef  string
		guideline string
		issues    []string
	}{
		{
			file: "ubl-min.xml", sx: SyntaxUBL, id: "2018-112", currency: "SEK",
			payable: 500, lines: 1, seller: "Säljbolaget AB", buyerRef: "ACE22",
		},
		{
			file: "ubl-peppol.xml", sx: SyntaxUBL, id: "2026-114", currency: "SEK",
			payable: 1625, lines: 1, seller: "Sjalte AB", buyerRef: "ACE22",
		},
		{
			file: "cii-xrechnung.xml", sx: SyntaxCII, id: "150377292", currency: "EUR",
			payable: 385544.60, lines: 2, seller: "XX", buyerRef: "XX", guideline: "urn:cen.eu:en16931:2017",
			issues: []string{"ram:AdditionalReferencedDocument"},
		},
	}
	for _, tc := range tbl {
		rep := readFor(t, tc.sx, sample(t, tc.file))
		in := rep.Invoice
		if in.ID != tc.id {
			t.Errorf("%s: id = %q, want %q", tc.file, in.ID, tc.id)
		}
		if in.Currency != tc.currency {
			t.Errorf("%s: currency = %q, want %q", tc.file, in.Currency, tc.currency)
		}
		if p := in.Totals.Payable; p == nil || *p != tc.payable {
			t.Errorf("%s: payable = %v, want %v", tc.file, p, tc.payable)
		}
		if len(in.Lines) != tc.lines {
			t.Errorf("%s: %d lines, want %d", tc.file, len(in.Lines), tc.lines)
		}
		if in.Seller.Name != tc.seller {
			t.Errorf("%s: seller = %q, want %q", tc.file, in.Seller.Name, tc.seller)
		}
		if in.BuyerReference != tc.buyerRef {
			t.Errorf("%s: buyer reference = %q, want %q", tc.file, in.BuyerReference, tc.buyerRef)
		}
		if tc.guideline != "" && in.Guideline != tc.guideline {
			t.Errorf("%s: guideline = %q, want %q", tc.file, in.Guideline, tc.guideline)
		}
		for _, w := range tc.issues {
			if !slices.ContainsFunc(rep.Issues, func(s string) bool { return strings.Contains(s, w) }) {
				t.Errorf("%s: no issue names %s in %s", tc.file, w, strings.Join(rep.Issues, "; "))
			}
		}
		if len(tc.issues) == 0 && len(rep.Issues) > 0 {
			t.Errorf("%s: issues: %s", tc.file, strings.Join(rep.Issues, "; "))
		}
	}
}

func TestReadThenConvertStaysStable(t *testing.T) {
	// The work the reader is for: a customer's CII document becomes a model,
	// and the model becomes a UBL document that reads back as the same invoice.
	first := readFor(t, SyntaxCII, sample(t, "cii-xrechnung.xml")).Invoice
	second := readFor(t, SyntaxUBL, renderFor(t, SyntaxUBL, first)).Invoice
	if d := modelDiff(expectFor(t, first, SyntaxUBL), second); d != "" {
		t.Errorf("CII to UBL changed the invoice: %s", d)
	}
	if second.ID != first.ID || second.Currency != first.Currency {
		t.Errorf("identity lost: %q/%q then %q/%q", first.ID, first.Currency, second.ID, second.Currency)
	}
	if len(second.Lines) != len(first.Lines) {
		t.Errorf("%d lines became %d", len(first.Lines), len(second.Lines))
	}
}

// TestConversionKeepsTheStatedAmounts is the defect a customer's XRechnung
// exposed. A charge that states a percentage, the amount it was taken on and
// the resulting amount has said all three; rewriting the document in the other
// syntax has to say them too. Deriving the amount from the percentage and a
// base the document never named invents money, and it breaks BR-CO-12, because
// the charge total the document states no longer matches the charges written.
func TestConversionKeepsTheStatedAmounts(t *testing.T) {
	first := readFor(t, SyntaxCII, sample(t, "cii-xrechnung.xml")).Invoice
	second := readFor(t, SyntaxUBL, renderFor(t, SyntaxUBL, first)).Invoice

	// BT-99 with BT-100: the amounts the document states, and the base each
	// percentage was taken on.
	want := []struct{ amount, basis float64 }{{15894.27, 83654.15}, {33349.38, 252646.80}}
	if len(second.Charges) != len(want) {
		t.Fatalf("%d document level charges, want %d", len(second.Charges), len(want))
	}
	for i, w := range want {
		c := second.Charges[i]
		if c.Amount != w.amount {
			t.Errorf("charge %d: amount = %s, want the %s the document states", i, Money(c.Amount), Money(w.amount))
		}
		if c.Basis == nil || *c.Basis != w.basis {
			t.Errorf("charge %d: basis = %v, want the %s the document states", i, c.Basis, Money(w.basis))
		}
	}
	// BT-142 on the first line, which states the same charge at line level.
	if len(second.Lines) == 0 || len(second.Lines[0].Charges) == 0 {
		t.Fatalf("the first line lost its charge")
	}
	if got := second.Lines[0].Charges[0]; got.Amount != want[0].amount {
		t.Errorf("line charge: amount = %s, want %s", Money(got.Amount), Money(want[0].amount))
	}
	// BR-CO-12: what was written has to add up to the stated total.
	ct := second.Totals.ChargeTotal
	if ct == nil || *ct != 49243.65 {
		t.Fatalf("charge total = %v, want 49243.65", ct)
	}
	var sum float64
	for _, c := range second.Charges {
		sum += c.Amount
	}
	if Money(sum) != Money(*ct) {
		t.Errorf("charges sum to %s, but BT-108 says %s", Money(sum), Money(*ct))
	}
}

// TestConversionKeepsTheRegistrations is the third thing the same customer's
// XRechnung exposed. Its seller has no VAT number: it holds a fiscal number
// (BT-31 on the FC basis) and a commercial register entry (BT-30, with the
// register named by BT-29's scheme). The old writer filed the tax number under
// the legal entity, then overwrote it with the register entry, so the invoice
// came back with no tax registration at all - and BR-21 and friends are read
// from that element. Both syntaxes have to keep both numbers, and UBL has to
// say which register the tax number came from, because its only tax
// registration element is otherwise read as a VAT identifier.
func TestConversionKeepsTheRegistrations(t *testing.T) {
	first := readFor(t, SyntaxCII, sample(t, "cii-xrechnung.xml")).Invoice
	for _, sx := range syntaxes {
		doc := renderFor(t, sx, first)
		s := readFor(t, sx, doc).Invoice.Seller.Party
		want := TaxID{Value: "XX", Scheme: "FC"}
		if len(s.TaxIDs) != 1 || s.TaxIDs[0] != want {
			t.Errorf("%s: seller tax registrations = %+v, want the %v the document states", sx, s.TaxIDs, want)
		}
		if len(s.RegIDs) != 1 || s.RegIDs[0] != "XX" || s.RegScheme != "0189" {
			t.Errorf("%s: seller registration = %v on register %q, want XX on 0189", sx, s.RegIDs, s.RegScheme)
		}
	}
	ubl := string(renderFor(t, SyntaxUBL, first))
	if !strings.Contains(ubl, `<cbc:CompanyID schemeID="FC">`) {
		t.Errorf("the UBL tax registration carries no basis, so a reader has to take a fiscal number for a VAT number")
	}
	if !strings.Contains(ubl, `<cbc:CompanyID schemeID="0189">`) {
		t.Errorf("the UBL legal registration names no register")
	}
}

// TestUBLRefusesASecondTaxRegistration: EN 16931 gives a party one tax
// registration term, so a document holding both a VAT number and a fiscal
// number cannot become a UBL document without inventing or losing a number.
// UBL can only name the VAT one, so the other is an error a caller can act on,
// not something that quietly disappears. CII has room for both and says which
// is which.
func TestUBLRefusesASecondTaxRegistration(t *testing.T) {
	inv := fixture(t, "invoice-model.json")
	inv.Seller.TaxIDs = []TaxID{{Value: "GB123456789", Scheme: "VAT"}, {Value: "30/123/4567", Scheme: "FC"}}
	if _, err := RenderUBL(inv); err == nil || !strings.Contains(err.Error(), "30/123/4567") {
		t.Errorf("two tax registrations: %v, want an error naming the number with no term", err)
	}
	data, err := RenderCII(inv)
	if err != nil {
		t.Fatalf("cii: %v", err)
	}
	back := readFor(t, SyntaxCII, data).Invoice
	for _, want := range inv.Seller.TaxIDs {
		if !slices.Contains(back.Seller.TaxIDs, want) {
			t.Errorf("cii lost %v: %+v", want, back.Seller.TaxIDs)
		}
	}
}

// TestUBLRefusesTwoAccountIdentifiers: UBL gives a bank account one identifier
// where CII separates an IBAN from a private account number. Writing only the
// IBAN would drop a number the sender stated, so it is reported.
func TestUBLRefusesTwoAccountIdentifiers(t *testing.T) {
	inv := fixture(t, "invoice-model.json")
	inv.SellerAccount.OtherID = "1234567"
	if _, err := RenderUBL(inv); err == nil || !strings.Contains(err.Error(), "1234567") {
		t.Errorf("an IBAN and a private number: %v, want an error naming the private one", err)
	}
}
