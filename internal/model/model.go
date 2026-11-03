// Package model holds a syntax-neutral EN 16931 invoice model and the code
// that renders it as CII (UN/CEFACT, used by Factur-X and XRechnung) or UBL
// (OASIS, used by Peppol). It exists because validating an invoice is only half
// of what a system needs: it also has to produce one, and the arithmetic in
// EN 16931 (line sums, VAT breakdown, invoice totals) is exactly the part that
// software gets wrong.
//
// Amounts are float64 for convenience and are always rendered and compared
// rounded to two decimal places using half-up rounding, computed with exact
// rational arithmetic. Percentages may carry more decimals.
//
// Totals are optional: leave them empty and they are computed from the lines,
// document level allowances and charges, and the VAT breakdown. Supply them
// and they are emitted as given, so the validator can be used to check someone
// else's arithmetic.
package model

import (
	"errors"
	"fmt"
	"math"
	"math/big"
	"sort"
	"strconv"
	"strings"
)

// Syntax mirrors en16931.Syntax so the model package stays dependency free.
type Syntax string

const (
	SyntaxCII Syntax = "CII"
	SyntaxUBL Syntax = "UBL"
)

// Invoice is a syntax-neutral EN 16931 invoice or credit note.
type Invoice struct {
	ID          string   `json:"id"`          // BT-1
	IssueDate   string   `json:"issueDate"`   // BT-2, YYYY-MM-DD
	TypeCode    string   `json:"typeCode"`    // BT-3, UNTDID 1001 (380 invoice, 381 credit note)
	Currency    string   `json:"currency"`    // BT-5
	Notes       []string `json:"notes"`       // BT-22
	ProcessType string   `json:"processType"` // BT-23 (XRechnung / Peppol BIS profile)
	// Guideline is the specification the document claims to implement: the CII
	// GuidelineSpecifiedDocumentContextParameter and the UBL CustomizationID.
	Guideline string `json:"guideline,omitempty"`
	// BuyerReference is BT-10, the buyer's own reference for the document:
	// the Leitweg-ID in German public administration.
	BuyerReference string `json:"buyerReference,omitempty"`
	// DueDate is BT-9, the payment due date.
	DueDate string `json:"dueDate,omitempty"`
	// Accounting is BT-19, the buyer accounting reference. It is in no business
	// group of its own; the line level equivalent (BT-133) is Line.Account.
	Accounting *Account `json:"accounting,omitempty"`

	Seller Seller `json:"seller"` // BG-4
	Buyer  Party  `json:"buyer"`  // BG-7

	// Payee is BG-10. EN 16931 asks only for its name (BT-59) and its legal
	// registration identifier (BT-61); anything else on it, an address for
	// instance, is an extra that CII advises against (CII-SR-360) and UBL
	// allows.
	Payee         *Party         `json:"payee,omitempty"`         // BG-10
	SellerAccount *FinancialAcct `json:"sellerAccount,omitempty"` // BG-17
	// BuyerAccount is the payer's own account: BT-91 when a mandate debits
	// it, and beyond EN 16931 anywhere else, though CII has an element.
	BuyerAccount *FinancialAcct `json:"buyerAccount,omitempty"` // CII extra
	Payment      *Payment       `json:"payment,omitempty"`      // BG-16
	Delivery     *Delivery      `json:"delivery,omitempty"`     // BG-13
	References   []Reference    `json:"references,omitempty"`   // BG-3/BG-24
	Period       *Period        `json:"period,omitempty"`       // BG-14
	Terms        []string       `json:"terms,omitempty"`        // BT-20 payment terms

	Allowances []AllowanceCharge `json:"allowances,omitempty"` // BG-20
	Charges    []AllowanceCharge `json:"charges,omitempty"`    // BG-21
	Lines      []Line            `json:"lines"`                // BG-25

	// Derived amounts. Empty means "compute me".
	Totals       Totals    `json:"totals"`
	VATBreakdown []VATRate `json:"vatBreakdown,omitempty"` // BG-23; empty means derive from lines
	Prepaid      *float64  `json:"prepaid,omitempty"`      // BT-113 paid amount
	Rounding     *float64  `json:"rounding,omitempty"`     // BT-114 rounding amount
}

// Seller is a supplier party: a name, a postal address and at least one tax
// identifier are expected (BR-02, BR-06, BR-07, BR-08, BR-27, BR-28, BR-29).
type Seller struct {
	Party
	TradingName string `json:"tradingName,omitempty"` // BT-28, used instead of the legal name
}

// Party is the common part of a trade party.
type Party struct {
	Name string `json:"name"` // BT-27 / BT-44
	// Subdiv is the country subdivision, a second home for Address.Zone
	// (BT-39 seller, BT-54 buyer). Zone wins when both are set.
	Subdiv  string   `json:"subdivision,omitempty"`
	Address Address  `json:"address"`
	Contact *Contact `json:"contact,omitempty"`
	TaxIDs  []TaxID  `json:"taxIds,omitempty"` // BT-31, BT-32, BT-48
	RegIDs  []string `json:"regIds,omitempty"` // BT-30, BT-47
	// RegScheme names the register the legal registration identifiers come
	// from, as an ICD code: 0189 is the German commercial register, 0198 its
	// handroll number. Both syntaxes carry it on the identifier itself, CII
	// under SpecifiedLegalOrganization/ID/@schemeID and UBL under
	// PartyLegalEntity/cbc:CompanyID/@schemeID.
	RegScheme  string `json:"regScheme,omitempty"`
	EAddress   string `json:"eaddress,omitempty"` // BT-34, BT-49
	EAScheme   string `json:"easScheme,omitempty"`
	Identifier string `json:"identifier,omitempty"` // BT-29 seller, BT-46 buyer
	IDScheme   string `json:"idScheme,omitempty"`
}

// EASCode is the electronic address scheme to write with the electronic
// address: BT-34 for the seller, BT-49 for the buyer. An explicit scheme
// always wins. Failing that, an address that looks like an email address is
// given the International E-mail Address scheme, 0188, which both Peppol and
// the EN 16931 codelist accept. Anything else is left without a scheme rather
// than guessed at, because the code identifies a registration authority and a
// wrong one is worse than a missing one; Check reports the gap.
func (p Party) EASCode() string {
	if p.EAScheme != "" {
		return p.EAScheme
	}
	if strings.Contains(p.EAddress, "@") {
		return "0188"
	}
	return ""
}

// Address is a postal address: BG-5 for the seller, BG-8 for the buyer and
// BG-15 for the delivery (BR-57). One Address serves all three, so each field
// lists its seller, buyer and deliver-to terms in that order.
type Address struct {
	Line1   string `json:"line1,omitempty"`      // BT-35 / BT-50 / BT-75
	Line2   string `json:"line2,omitempty"`      // BT-36 / BT-51 / BT-76
	Line3   string `json:"line3,omitempty"`      // BT-162 / BT-163 / BT-165
	City    string `json:"city,omitempty"`       // BT-37 / BT-52 / BT-77
	Zone    string `json:"zone,omitempty"`       // BT-39 / BT-54 / BT-79 subdivision
	Postal  string `json:"postalCode,omitempty"` // BT-38 / BT-53 / BT-78
	Country string `json:"country"`              // BT-40 / BT-55 / BT-80, ISO 3166-1 alpha-2
}

// Contact is BG-6 for the seller and BG-9 for the buyer: contact point,
// telephone and email are BT-41, BT-42 and BT-43 on the seller's side, and
// BT-56, BT-57 and BT-58 on the buyer's.
type Contact struct {
	Name  string `json:"name,omitempty"`  // BT-41
	Phone string `json:"phone,omitempty"` // BT-42
	Email string `json:"email,omitempty"` // BT-43
}

// TaxID is a VAT or other tax identifier with its scheme.
type TaxID struct {
	Value string `json:"value"`
	// Scheme is the basis the number was issued against: VAT for a VAT
	// number (CII writes that basis as VA), FC for a fiscal number, which a
	// German profile uses where it has no VAT number. Empty means the source
	// document did not say.
	Scheme string `json:"scheme,omitempty"`
}

// FinancialAcct is a payment account: the payee's credit transfer account
// (BG-17: BT-84 identifier, BT-85 name, BT-86 service provider), or the
// account a direct debit mandate debits (BT-91).
type FinancialAcct struct {
	IBAN string `json:"iban,omitempty"`
	BIC  string `json:"bic,omitempty"`
	// Name is BT-85 for the seller's account. The paying account has no
	// name in EN 16931, so a name on BuyerAccount is an extra: CII flags it
	// as an advisory (CII-SR-382), UBL says nothing.
	Name    string `json:"name,omitempty"`
	OtherID string `json:"otherId,omitempty"`
}

// Payment is BG-16 (payment instructions), which holds the credit transfer
// group (BG-17), the card group (BG-18) and the direct debit mandate (BG-19).
// The CII flavour of the official schematron numbers the credit transfer group
// BG-16 and the card number BT-97 where UBL and the semantic model say BG-17
// and BT-87; only the prose of the rule differs, the check is the same.
type Payment struct {
	Code      string   `json:"code"`                // BT-81 UNTDID 4461
	Name      string   `json:"name,omitempty"`      // BT-82 payment means text
	RemitInfo string   `json:"remitInfo,omitempty"` // BT-83
	Card      *Card    `json:"card,omitempty"`      // BG-18
	Mandate   *Mandate `json:"mandate,omitempty"`   // BG-19
}

// Card is BG-18. EN 16931 defines the card number and the cardholder name and
// nothing else, so Type and Scheme are extras the syntaxes handle differently:
// CII has a card type code and no formula, UBL has a formula and no type code,
// and neither is a business term. The CII schematron advises against its card
// type code (CII-SR-373), because EN 16931 does not ask for it.
type Card struct {
	Pan    string `json:"pan"`              // BT-87 (BT-97 in CII prose), masked in practice
	Holder string `json:"holder,omitempty"` // BT-88
	Type   string `json:"type,omitempty"`   // CII only
	Scheme string `json:"scheme,omitempty"` // UBL only
}

// Mandate is BG-19 (direct debit).
type Mandate struct {
	ID   string         `json:"id,omitempty"`   // BT-89 mandate reference
	Bank *FinancialAcct `json:"bank,omitempty"` // BT-91 the debited account
}

// Delivery is BG-13.
type Delivery struct {
	Name       string  `json:"name,omitempty"`       // BT-70 deliver to party name
	ReceiverID string  `json:"receiverId,omitempty"` // BT-71 deliver to location id
	Address    Address `json:"address"`
	Period     *Period `json:"period,omitempty"` // CII only, see ArrivalDate
	// ShipTrade is an incoterm code and ShipPlace the place of delivery.
	// Neither is an EN 16931 business term: CII gives the pair one slot and
	// the schematron advises against it (CII-SR-293), UBL has no slot at all.
	ShipTrade string `json:"shipTrade,omitempty"`
	ShipPlace string `json:"shipPlace,omitempty"`
	// ArrivalDate is the actual delivery date (BT-72). DepartureDate, and a
	// Period on the delivery rather than on the invoice, are beyond EN 16931:
	// CII has elements for both but the official schematron advises against
	// them (CII-SR-319, CII-SR-329), and UBL has no slot for a departure
	// date at all. The invoicing period (BG-14, BT-73/BT-74) is Invoice.Period.
	ArrivalDate   string `json:"arrivalDate,omitempty"`
	DepartureDate string `json:"departureDate,omitempty"`
}

// Reference is a link to another document: BG-3 for the documents this
// invoice looks back on, BG-24 for supporting documents. Type picks the slot
// in each syntax: buyerOrder and purchase (the buyer's order, BT-13, though
// UBL pairs it with the buyer reference BT-10), salesOrder (BT-14), contract
// (BT-12), project (BT-11, whose name rides in Name), quotation (a term of
// its own in nobody's model), despatch advice (BT-16), deliveryNote (BT-15
// receiving advice) and preceding (BT-25, the invoice a credit note fixes).
type Reference struct {
	Type string `json:"type,omitempty"`
	ID   string `json:"id"`
	Name string `json:"name,omitempty"`
}

// Period is a start/end date pair.
type Period struct {
	Start string `json:"start,omitempty"`
	End   string `json:"end,omitempty"`
	Text  string `json:"text,omitempty"`
}

// AllowanceCharge is a document or line level discount or surcharge: BG-20
// allowances and BG-21 charges on the invoice, BG-27 and BG-28 on a line. At
// document level the amount is BT-92 for an allowance and BT-99 for a charge,
// the percentage BT-94 and BT-101, the reason BT-97/BT-98 and BT-104/BT-105.
// At line level they run BT-136 to BT-140 and BT-141 to BT-145.
type AllowanceCharge struct {
	// Amount is the amount taken off or added. Left at zero, the percentage
	// says what it is: the writers resolve it and write the figure down,
	// because both syntaxes require the amount.
	Amount float64 `json:"amount"`
	// Percent is BT-94, BT-101, BT-139 or BT-144.
	Percent *float64 `json:"percent,omitempty"`
	// Basis is the amount the percentage was taken on: BT-93, BT-100, BT-137
	// or BT-142. A document states it, and a conversion has to carry it, since
	// a percentage measured against a different base is different money. A
	// model built by hand usually leaves it out; the writers then measure the
	// percentage against the line net amount or the invoice total, which is
	// what the standard says a document level percentage means.
	Basis       *float64 `json:"basis,omitempty"`
	Reason      string   `json:"reason,omitempty"`     // BT-97 / BT-104 free text
	ReasonCode  string   `json:"reasonCode,omitempty"` // BT-98 / BT-105 UNTDID 5189
	TaxCategory string   `json:"taxCategory,omitempty"`
	TaxRate     *float64 `json:"taxRate,omitempty"`
}

// Line is an invoice line (BG-25).
type Line struct {
	ID          string            `json:"id"`
	Note        string            `json:"note,omitempty"` // BT-127
	Quantity    float64           `json:"quantity"`       // BT-129
	Unit        string            `json:"unit,omitempty"`
	UnitCode    string            `json:"unitCode,omitempty"` // BT-130 UNTDID 6
	GrossPrice  float64           `json:"grossPrice,omitempty"`
	NetPrice    float64           `json:"netPrice"` // BT-146
	Allowances  []AllowanceCharge `json:"allowances,omitempty"`
	Charges     []AllowanceCharge `json:"charges,omitempty"`
	TaxCategory string            `json:"taxCategory"` // BT-151
	TaxRate     float64           `json:"taxRate"`     // BT-152
	TaxExempt   *TaxExempt        `json:"taxExempt,omitempty"`
	Item        Item              `json:"item"`
	Period      *Period           `json:"period,omitempty"` // BG-26
	Attributes  []Attribute       `json:"attributes,omitempty"`
	// Extension is the line net amount (BT-131); empty means compute it from
	// quantity and price.
	Extension *float64 `json:"extension,omitempty"`
	Account   *Account `json:"account,omitempty"`
}

// Account is the buyer accounting reference: BT-19 on the invoice, BT-133 on
// a line. EN 16931 defines the reference and no type for it, so Type is an
// extra: CII has a type code but the official schematron advises against it
// (CII-SR-429).
type Account struct {
	ID   string `json:"id,omitempty"`
	Type string `json:"type,omitempty"` // CII only, advisory
}

// TaxExempt explains a non-standard VAT category: the exemption reason text
// (BT-120) and code (BT-121). Amount carries an exemption amount a caller
// already holds; neither syntax writes it.
type TaxExempt struct {
	Reason     string   `json:"reason,omitempty"`
	ReasonCode string   `json:"reasonCode,omitempty"`
	Amount     *float64 `json:"amount,omitempty"`
}

// Item is BG-31 (invoice line item).
type Item struct {
	Name        string      `json:"name"`                  // BT-153
	Description string      `json:"description,omitempty"` // BT-154
	SellerID    string      `json:"sellerId,omitempty"`    // BT-155
	CustomerID  string      `json:"customerId,omitempty"`  // BT-156
	StandardID  string      `json:"standardId,omitempty"`  // BT-157 (EA/VT/…)
	StdScheme   string      `json:"stdScheme,omitempty"`
	ClassIDs    []string    `json:"classIds,omitempty"` // BT-158 commodity classification
	ClassScheme string      `json:"classScheme,omitempty"`
	Origin      string      `json:"origin,omitempty"` // BT-159 country code
	Units       string      `json:"units,omitempty"`  // BT-130 fallback
	Price       *ItemPrice  `json:"price,omitempty"`
	Attributes  []Attribute `json:"attributes,omitempty"`
}

// ItemPrice is the price details group of a line (BG-29): the quantity the
// price is quoted for (BT-149) and the amount knocked off the gross price
// (BT-147). The prices themselves are on the line, as Line.NetPrice (BT-146)
// and Line.GrossPrice (BT-148).
type ItemPrice struct {
	BaseQty   float64  `json:"baseQuantity,omitempty"`
	BaseUnit  string   `json:"baseUnitCode,omitempty"`
	Allowance *float64 `json:"allowance,omitempty"`
}

// Attribute is an item attribute: BG-32, whose name is BT-160 and value
// BT-161. Line attributes are written as attributes of their line's item.
type Attribute struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

// Totals holds the derived invoice amounts. A nil field is computed.
type Totals struct {
	LineTotal      *float64 `json:"lineTotal,omitempty"`      // BT-106
	AllowanceTotal *float64 `json:"allowanceTotal,omitempty"` // BT-107
	ChargeTotal    *float64 `json:"chargeTotal,omitempty"`    // BT-108
	TaxExclusive   *float64 `json:"taxExclusive,omitempty"`   // BT-109
	TaxTotal       *float64 `json:"taxTotal,omitempty"`       // BT-110
	TaxInclusive   *float64 `json:"taxInclusive,omitempty"`   // BT-112
	Payable        *float64 `json:"payable,omitempty"`        // BT-115
}

// VATRate is one line of the VAT breakdown (BG-23).
type VATRate struct {
	Category string     `json:"category"`          // BT-118
	Percent  float64    `json:"percent"`           // BT-119
	Taxable  float64    `json:"taxable,omitempty"` // BT-116; 0 means derive
	Amount   float64    `json:"amount,omitempty"`  // BT-117; 0 means derive
	Exempt   *TaxExempt `json:"exempt,omitempty"`
}

// Money formats an amount the way both syntaxes require it: two decimals.
// The formatting is done on exact rationals rather than with
// fmt.Sprintf("%.2f"), because a binary float is never exactly 0.15 and the
// cent boundary is where invoice arithmetic goes wrong.
func Money(v float64) string { return money(v, 2) }

// Rate formats a percentage: no forced decimals, no exponent, and no
// artefacts of binary floating point (21 prints as "21", not "20.999999").
func Rate(v float64) string { return rate(v) }

func money(v float64, dp int) string {
	r := new(big.Rat).SetFloat64(v)
	if r == nil { // NaN or Inf: nothing sensible to emit
		return "0"
	}
	return fixed(roundUnits(r, dp), dp)
}

// roundUnits rounds to dp decimals and returns the number of minor units.
func roundUnits(r *big.Rat, dp int) *big.Int {
	return floorRat(new(big.Rat).Add(new(big.Rat).Mul(r, pow10(dp)), halfRat()))
}

func halfRat() *big.Rat { return new(big.Rat).SetFrac64(1, 2) }

func pow10(n int) *big.Rat {
	d := big.NewInt(1)
	for range n {
		d.Mul(d, big.NewInt(10))
	}
	return new(big.Rat).SetInt(d)
}

// fixed renders a count of minor units as a decimal string.
func fixed(units *big.Int, dp int) string {
	s := units.String()
	neg := strings.HasPrefix(s, "-")
	if neg {
		s = s[1:]
	}
	if dp == 0 {
		if neg {
			return "-" + s
		}
		return s
	}
	for len(s) <= dp {
		s = "0" + s
	}
	out := s[:len(s)-dp] + "." + s[len(s)-dp:]
	if neg {
		out = "-" + out
	}
	return out
}

// rate renders a percentage with the shortest exact decimal form.
func rate(v float64) string {
	if math.IsNaN(v) || math.IsInf(v, 0) {
		return "0"
	}
	return strconv.FormatFloat(v, 'f', -1, 64)
}

// ratOf converts a decimal amount to an exact rational (rounded to cents).
func ratOf(v float64) *big.Rat { return round2(new(big.Rat).SetFloat64(v)) }

func round2(r *big.Rat) *big.Rat { return roundScale(r, 100) }

// roundScale rounds half away from zero to the given scale.
func roundScale(r *big.Rat, scale int64) *big.Rat {
	scaled := new(big.Rat).Mul(r, new(big.Rat).SetInt64(scale))
	// floor(x + 1/2) implements half-up for positives and half-away-from-zero
	// for negatives, which matches how invoice amounts are rounded in practice.
	half := new(big.Rat).SetInt64(1)
	half.Quo(half, new(big.Rat).SetInt64(2))
	fl := floorRat(new(big.Rat).Add(scaled, half))
	out := new(big.Rat).SetInt(fl)
	return out.Quo(out, new(big.Rat).SetInt64(scale))
}

func floorRat(r *big.Rat) *big.Int {
	q := new(big.Int).Quo(r.Num(), r.Denom())
	if r.Sign() < 0 && !r.IsInt() {
		q.Sub(q, big.NewInt(1))
	}
	return q
}

// Sum adds rationals.
func Sum(rs ...*big.Rat) *big.Rat {
	t := new(big.Rat)
	for _, r := range rs {
		if r != nil {
			t.Add(t, r)
		}
	}
	return t
}

// setSubtotals writes the three sums and the invoice total before VAT, leaving
// any figure the caller already supplied alone.
func setSubtotals(t *Totals, lineTotal, chargeTotal, allowTotal *big.Rat) {
	if t.LineTotal == nil {
		t.LineTotal = ratFloat(lineTotal)
	}
	if t.ChargeTotal == nil {
		t.ChargeTotal = ratFloat(chargeTotal)
	}
	if t.AllowanceTotal == nil {
		t.AllowanceTotal = ratFloat(allowTotal)
	}
	if t.TaxExclusive == nil {
		sum := new(big.Rat).Set(ratOf(*t.LineTotal))
		sum.Add(sum, ratOf(*t.ChargeTotal))
		sum.Sub(sum, ratOf(*t.AllowanceTotal))
		t.TaxExclusive = ratFloat(sum)
	}
}

// ratFloat converts a rational to the float the model stores currency values in.
func ratFloat(r *big.Rat) *float64 {
	f, _ := r.Float64()
	return &f
}

// deriveLine fills in what a line does not state: its unit of measure and its
// total, both of which a reader is entitled to expect, and adds the total to
// lineTotal.
func deriveLine(ln *Line, lineTotal *big.Rat) {
	if ln.UnitCode == "" {
		// BT-130 is mandatory on every line (BR-23); an item that
		// declares its own unit of measure supplies it.
		ln.UnitCode = ln.Item.Units
		if ln.UnitCode == "" {
			ln.UnitCode = "H8" // piece
		}
	}
	if ln.Extension == nil {
		// BR-CO-24/BT-131: the line total is the quantity times the net
		// price, and a percentage line allowance or charge is a percentage
		// of that same amount.
		base := lineBase(*ln)
		ext := new(big.Rat).Set(base)
		for _, a := range ln.Allowances {
			ext.Sub(ext, allowanceAmount(a, base))
		}
		for _, c := range ln.Charges {
			ext.Add(ext, allowanceAmount(c, base))
		}
		f, _ := ext.Float64()
		ln.Extension = &f
	}
	lineTotal.Add(lineTotal, ratOf(*ln.Extension))
}

// Derive fills in the totals and the VAT breakdown that were left empty.
// It returns an error only for a model that cannot be computed at all (no
// lines, a percentage that is not a number); rule violations are left for the
// validator to report.
func (in *Invoice) Derive() error {
	if len(in.Lines) == 0 {
		return errors.New("model: invoice has no lines")
	}
	if in.Currency == "" {
		in.Currency = "EUR"
	}
	if in.TypeCode == "" {
		in.TypeCode = "380"
	}
	lineTotal := new(big.Rat)
	for i := range in.Lines {
		deriveLine(&in.Lines[i], lineTotal)
	}

	// A document level allowance or charge carries its own VAT category and
	// rate. If the caller left them out and every line agrees, that is the
	// only defensible answer; otherwise leave them empty and let the
	// validator report the gap rather than invent a rate.
	if cat, pct, ok := singleRate(in); ok {
		for i := range in.Allowances {
			setRate(&in.Allowances[i], cat, pct)
		}
		for i := range in.Charges {
			setRate(&in.Charges[i], cat, pct)
		}
	}

	// A document level percentage is a percentage of the invoice total before
	// VAT (BT-109). Charges first, then allowances, which is how the
	// implementations that get this right order it: a discount percentage is
	// taken after the surcharges have been added.
	chargeTotal := new(big.Rat)
	for _, c := range in.Charges {
		chargeTotal.Add(chargeTotal, allowanceAmount(c, lineTotal))
	}
	allowTotal := new(big.Rat)
	chargeBasis := new(big.Rat).Add(lineTotal, chargeTotal)
	for _, a := range in.Allowances {
		allowTotal.Add(allowTotal, allowanceAmount(a, chargeBasis))
	}

	setSubtotals(&in.Totals, lineTotal, chargeTotal, allowTotal)

	if len(in.VATBreakdown) == 0 {
		in.VATBreakdown = deriveVAT(in)
	}

	taxTotal := new(big.Rat)
	for i := range in.VATBreakdown {
		v := &in.VATBreakdown[i]
		if v.Category == "" {
			v.Category = "S"
		}
		if v.Taxable == 0 {
			v.Taxable = basisOf(in, v.Category, v.Percent)
		}
		if v.Amount == 0 {
			r := new(big.Rat).SetFloat64(v.Taxable)
			r.Mul(r, new(big.Rat).SetFloat64(v.Percent))
			r.Quo(r, big.NewRat(100, 1))
			f, _ := round2(r).Float64()
			v.Amount = f
		}
		taxTotal.Add(taxTotal, ratOf(v.Amount))
	}

	if in.Totals.TaxTotal == nil {
		f, _ := taxTotal.Float64()
		in.Totals.TaxTotal = &f
	}
	if in.Totals.TaxInclusive == nil {
		t := new(big.Rat).Set(ratOf(*in.Totals.TaxExclusive))
		t.Add(t, ratOf(*in.Totals.TaxTotal))
		f, _ := t.Float64()
		in.Totals.TaxInclusive = &f
	}
	if in.Totals.Payable == nil {
		// BR-CO-16: amount due = total with VAT - paid amount + rounding.
		t := new(big.Rat).Set(ratOf(*in.Totals.TaxInclusive))
		if in.Prepaid != nil {
			t.Sub(t, ratOf(*in.Prepaid))
		}
		if in.Rounding != nil {
			t.Add(t, ratOf(*in.Rounding))
		}
		f, _ := t.Float64()
		in.Totals.Payable = &f
	}
	return nil
}

// singleRate returns the VAT category and rate when every line uses the same
// pair, so that document level allowances and charges can inherit them.
func singleRate(in *Invoice) (string, float64, bool) {
	if len(in.Lines) == 0 {
		return "", 0, false
	}
	cat, pct := catOfLine(in.Lines[0]), in.Lines[0].TaxRate
	for _, ln := range in.Lines[1:] {
		if catOfLine(ln) != cat || ln.TaxRate != pct {
			return "", 0, false
		}
	}
	return cat, pct, true
}

func catOfLine(ln Line) string {
	if ln.TaxCategory == "" {
		return "S"
	}
	return ln.TaxCategory
}

func setRate(a *AllowanceCharge, cat string, pct float64) {
	if a.TaxCategory == "" {
		a.TaxCategory = cat
	}
	if a.TaxRate == nil {
		v := pct
		a.TaxRate = &v
	}
}

// lineBase is the amount a line level percentage is taken from: BT-131 before
// the line allowances and charges (BR-CO-24).
func lineBase(ln Line) *big.Rat {
	q := new(big.Rat).SetFloat64(ln.Quantity)
	p := new(big.Rat).SetFloat64(ln.NetPrice)
	return round2(new(big.Rat).Mul(q, p))
}

// allowanceAmount resolves an allowance or charge to an amount: an explicit
// amount wins, otherwise the percentage of the base. The base is a rational so
// that a percentage chain does not accumulate binary error.
func allowanceAmount(a AllowanceCharge, base *big.Rat) *big.Rat {
	if a.Amount != 0 {
		return ratOf(a.Amount)
	}
	if b := acBasis(a, base); b != nil && a.Percent != nil {
		r := new(big.Rat).Set(b)
		r.Mul(r, new(big.Rat).SetFloat64(*a.Percent))
		r.Quo(r, big.NewRat(100, 1))
		return round2(r)
	}
	return new(big.Rat)
}

// acBasis is the amount a percentage is measured against: the base the model
// states (BT-93, BT-100, BT-137, BT-142) when it states one, and otherwise the
// base the position implies, which the caller passes in.
func acBasis(a AllowanceCharge, base *big.Rat) *big.Rat {
	if a.Basis != nil {
		return ratOf(*a.Basis)
	}
	return base
}

// acBasisOf is the same choice for a writer, which holds the implied base as a
// plain figure: the base amount to write beside the percentage.
func acBasisOf(a AllowanceCharge, derived float64) float64 {
	if a.Basis != nil {
		return *a.Basis
	}
	return derived
}

// priceParts resolves the price of a line into the three figures the syntaxes
// need: the net price (BT-146), the gross price (BT-148) and the discount
// between them (BT-147). A caller usually fills in only one price, and the
// other has to be worked out, because each syntax expresses the pair in its own
// way: CII hangs the discount off the gross price element, so a discount with no
// gross price has nowhere to go, while UBL hangs it off the net one, so a gross
// price with no discount is dropped. Both figures are therefore written whenever
// they say anything. Gross less discount must be the net price, and figures that
// do not add up are reported rather than quietly reconciled.
func priceParts(ln Line) (net, gross, disc float64, bad string) {
	n := ratOf(ln.NetPrice)
	g := ratOf(ln.GrossPrice)
	d := new(big.Rat)
	if p := ln.Item.Price; p != nil && p.Allowance != nil {
		d = ratOf(*p.Allowance)
	}
	switch {
	case g.Sign() == 0 && d.Sign() > 0:
		g = new(big.Rat).Add(n, d)
	case g.Sign() == 0:
		g = new(big.Rat).Set(n)
	case d.Sign() == 0 && g.Cmp(n) > 0:
		d = new(big.Rat).Sub(g, n)
	}
	if diff := new(big.Rat).Sub(new(big.Rat).Sub(g, d), n); diff.Sign() != 0 {
		fg, _ := g.Float64()
		fd, _ := d.Float64()
		bad = fmt.Sprintf("line %s: a gross price of %s less a discount of %s is not a net price of %s",
			ln.ID, Money(fg), Money(fd), Money(ln.NetPrice))
	}
	net, _ = n.Float64()
	gross, _ = g.Float64()
	disc, _ = d.Float64()
	return net, gross, disc, bad
}

// deriveVAT groups the lines and document level allowances and charges by VAT
// category and rate, and returns the breakdown in a stable order.
func deriveVAT(in *Invoice) []VATRate {
	type key struct {
		cat string
		pct float64
	}
	basis := map[key]*big.Rat{}
	exempt := map[key]*TaxExempt{}
	var keys []key
	add := func(k key, amt *big.Rat, te *TaxExempt) {
		b, ok := basis[k]
		if !ok {
			b = new(big.Rat)
			basis[k] = b
			keys = append(keys, k)
		}
		b.Add(b, amt)
		if te != nil && exempt[k] == nil {
			c := *te
			exempt[k] = &c
		}
	}
	for _, ln := range in.Lines {
		cat := ln.TaxCategory
		if cat == "" {
			cat = "S"
		}
		k := key{cat, ln.TaxRate}
		add(k, ratOf(*ln.Extension), ln.TaxExempt)
	}
	// A document level allowance or charge is taxable in its own right, so it
	// moves the breakdown: charges up, allowances down.
	docBasis := new(big.Rat).Add(lineTotalBasis(in), chargeTotalBasis(in))
	for _, ac := range in.Charges {
		add(key{catOf(ac), pctOf(ac)}, allowanceAmount(ac, docBasis), nil)
	}
	for _, ac := range in.Allowances {
		add(key{catOf(ac), pctOf(ac)}, new(big.Rat).Neg(allowanceAmount(ac, docBasis)), nil)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].cat != keys[j].cat {
			return keys[i].cat < keys[j].cat
		}
		return keys[i].pct < keys[j].pct
	})
	out := make([]VATRate, 0, len(keys))
	for _, k := range keys {
		f, _ := round2(basis[k]).Float64()
		out = append(out, VATRate{Category: k.cat, Percent: k.pct, Taxable: f, Exempt: exempt[k]})
	}
	return out
}

// lineTotalBasis and chargeTotalBasis expose the totals that a document level
// percentage is measured against, for use before Totals has been filled in.
func lineTotalBasis(in *Invoice) *big.Rat {
	if in.Totals.LineTotal != nil {
		return ratOf(*in.Totals.LineTotal)
	}
	t := new(big.Rat)
	for _, ln := range in.Lines {
		if ln.Extension != nil {
			t.Add(t, ratOf(*ln.Extension))
		}
	}
	return t
}

func chargeTotalBasis(in *Invoice) *big.Rat {
	if in.Totals.ChargeTotal != nil {
		return ratOf(*in.Totals.ChargeTotal)
	}
	t := new(big.Rat)
	for _, ac := range in.Charges {
		t.Add(t, allowanceAmount(ac, lineTotalBasis(in)))
	}
	return t
}

// basisOf returns the taxable amount for a category and rate, used when a
// breakdown line names a taxable amount of its own but leaves it at zero.
func basisOf(in *Invoice, cat string, pct float64) float64 {
	t := new(big.Rat)
	for _, ln := range in.Lines {
		c := ln.TaxCategory
		if c == "" {
			c = "S"
		}
		if c == cat && ln.TaxRate == pct {
			t.Add(t, ratOf(*ln.Extension))
		}
	}
	docBasis := new(big.Rat).Add(lineTotalBasis(in), chargeTotalBasis(in))
	for _, ac := range in.Charges {
		if catOf(ac) == cat && pctOf(ac) == pct {
			t.Add(t, allowanceAmount(ac, docBasis))
		}
	}
	for _, ac := range in.Allowances {
		if catOf(ac) == cat && pctOf(ac) == pct {
			t.Sub(t, allowanceAmount(ac, docBasis))
		}
	}
	f, _ := round2(t).Float64()
	return f
}

func catOf(a AllowanceCharge) string {
	if a.TaxCategory != "" {
		return a.TaxCategory
	}
	return "S"
}

func pctOf(a AllowanceCharge) float64 {
	if a.TaxRate != nil {
		return *a.TaxRate
	}
	return 0
}

// Problem is a model level error: something the model can see is missing
// before any XML is produced. The messages name the EN 16931 business rule so
// the caller can act on them.
type Problem struct {
	Rule  string
	Field string
	Text  string
}

func (p Problem) Error() string {
	return fmt.Sprintf("%s: %s (%s)", p.Rule, p.Text, p.Field)
}

// Check reports the obvious modelling mistakes. It is deliberately small: the
// validator holds 1,700+ rules and is the authority, so Check only covers what
// would otherwise produce an unusable document.
func (in *Invoice) Check() []Problem {
	var out []Problem
	add := func(rule, field, format string, args ...any) {
		out = append(out, Problem{Rule: rule, Field: field, Text: fmt.Sprintf(format, args...)})
	}
	if in.ID == "" {
		add("BR-02", "id", "invoice number is missing")
	}
	if in.IssueDate == "" {
		add("BR-03", "issueDate", "issue date is missing")
	}
	if in.Currency == "" {
		add("BR-04", "currency", "document currency is missing")
	}
	if in.Seller.Name == "" {
		add("BR-02", "seller.name", "seller name is missing")
	}
	if in.Seller.Address.Country == "" {
		add("BR-06", "seller.address.country", "seller country is missing")
	}
	if in.Buyer.Name == "" {
		add("BR-03", "buyer.name", "buyer name is missing")
	}
	// An electronic address with no scheme cannot be routed or looked up, and
	// both Peppol and the norm say so. Only the parties that supplied an
	// address are asked about: the address itself is optional in the norm.
	for _, p := range []struct {
		field string
		party Party
		rule  string
		bt    string
	}{
		{"seller", in.Seller.Party, "BR-62", "BT-34"},
		{"buyer", in.Buyer, "BR-63", "BT-49"},
	} {
		if p.party.EAddress != "" && p.party.EASCode() == "" {
			add(p.rule, p.field+".eaddress",
				"electronic address (%s) %q has no scheme identifier: set easScheme from UN/ECE Recommendation 20, or use an email address, which is given 0188",
				p.bt, p.party.EAddress)
		}
	}
	if in.Buyer.Address.Country == "" {
		add("BR-07", "buyer.address.country", "buyer country is missing")
	}
	if len(in.Lines) == 0 {
		add("BR-16", "lines", "an invoice must have at least one line")
	}
	for i, ln := range in.Lines {
		if ln.ID == "" {
			add("BR-21", fmt.Sprintf("lines[%d].id", i), "line reference is missing")
		}
		if ln.Quantity == 0 {
			add("BR-22", fmt.Sprintf("lines[%d].quantity", i), "line quantity is missing")
		}
		if ln.Item.Name == "" {
			add("BR-21", fmt.Sprintf("lines[%d].item.name", i), "item name is missing")
		}
		if ln.TaxCategory == "" {
			add("BR-CO-18", fmt.Sprintf("lines[%d].taxCategory", i), "line VAT category is missing")
		}
	}
	if in.Payment == nil {
		add("BR-81", "payment", "specification of payment means is missing")
	} else if in.Payment.Code == "" {
		add("BR-80", "payment.code", "payment means code is missing")
	}
	if in.Delivery == nil && in.Buyer.Address.Country == "" {
		add("BR-07", "delivery", "either a delivery address or a buyer country is required")
	}
	if !strings.Contains(" 380 381 382 383 384 385 386 387 388 389 390 393 575 623 71 80 81 82 84 86 87 102 132 143 144 146 150 151 152 153 154 155 156 290 291 292 293 294 295 296 304 308 309 ", " "+in.TypeCode+" ") {
		add("BR-03", "typeCode", "%q is not a known UNTDID 1001 invoice type", in.TypeCode)
	}
	return out
}
