package model

// OASIS UBL Invoice 2.1 output, the second syntax of EN 16931. Element order
// is not cosmetic here: the UBL schema is a sequence-based schema, so a
// document that gets the order wrong is simply invalid.
//
// The rules in the EES-EN16931 UBL schematron split into fatal assertions and
// advisory ones (flag="warning"). The renderer follows the fatal ones and
// steers clear of the advisory ones where it can, which means a handful of
// model terms have no UBL home and are dropped, while others that cannot be
// squeezed into the single element UBL gives them are reported as errors.

const (
	// NSUBL, NSCREDIT, NSCAC and NSCBC are the UBL 2.1 namespaces of the
	// invoice document, the credit note document, and their aggregate and
	// basic components.
	NSUBL    = "urn:oasis:names:specification:ubl:schema:xsd:Invoice-2"
	NSCREDIT = "urn:oasis:names:specification:ubl:schema:xsd:CreditNote-2"
	NSCAC    = "urn:oasis:names:specification:ubl:schema:xsd:CommonAggregateComponents-2"
	NSCBC    = "urn:oasis:names:specification:ubl:schema:xsd:CommonBasicComponents-2"

	// UBLProfile is the default specification identifier (BT-24, BR-01).
	UBLProfile = "urn:cen.eu:en16931:2017"

	// ublTaxSchemeID is the tax scheme every VAT term in a document carries.
	ublTaxSchemeID = "VAT"
	// ublFiscalSchemeID is the tax scheme of a registration number that is
	// not a VAT number: BT-32, a country's own number for the trader, which
	// CII labels FC and a German invoice fills with its Steuernummer.
	ublFiscalSchemeID = "TAX"
)

// ublCreditCodes holds the UNTDID 1001 codes that BR-CL-01 allows only in a
// credit note. A model invoice carrying one of them is written as a UBL
// CreditNote document, which is where those codes belong.
var ublCreditCodes = map[string]bool{
	"81": true, "83": true, "261": true, "262": true, "296": true, "308": true,
	"381": true, "396": true, "420": true, "458": true, "502": true,
	"503": true, "532": true,
}

func isCreditType(code string) bool { return ublCreditCodes[code] }

// amt writes an amount. The currencyID attribute is required by the schema on
// every amount, so there is no variant of this that leaves it out.
func (x *xw) amt(name, cur string, v float64) {
	x.elem(name, Money(v), attr{"currencyID", cur})
}

// pamt writes an amount the caller expects to have been calculated already.
func (x *xw) pamt(name, cur string, v *float64) {
	if v == nil {
		x.bad(name + " is missing")
		return
	}
	x.amt(name, cur, *v)
}

// oamt writes an optional amount.
func (x *xw) oamt(name, cur string, v *float64) {
	if v == nil {
		return
	}
	x.amt(name, cur, *v)
}

// qty writes a quantity. unitCode is required by the schema, so a quantity
// without a unit is reported rather than written without the attribute.
func (x *xw) qty(name string, v float64, code string) {
	if code == "" {
		x.bad(name + " has no unit code")
		return
	}
	x.elem(name, Rate(v), attr{"unitCode", code})
}

// ublDate writes a UBL calendar date, which unlike its CII counterpart is
// plain text in ISO form.
func (x *xw) ublDate(name, v string) {
	x.opt(name, isoDate(v))
}

// leaf writes a wrapper element that carries nothing but one basic component,
// the shape UBL uses for elements such as cac:PartyName or
// cac:BuyersItemIdentification.
func leaf(x *xw, wrap, name, v string, a ...attr) {
	if v == "" {
		return
	}
	x.start(wrap)
	x.elem(name, v, a...)
	x.end(wrap)
}

// ublTaxScheme writes the tax scheme of a category. UBL-CR-484 and its
// siblings allow nothing but the identifier inside the scheme.
func (x *xw) ublTaxScheme() { x.ublTaxSchemeKind(ublTaxSchemeID) }

// ublTaxSchemeKind writes the tax scheme that says which register the
// identifier around it came from.
func (x *xw) ublTaxSchemeKind(id string) {
	x.start("cac:TaxScheme")
	x.elem("cbc:ID", id)
	x.end("cac:TaxScheme")
}

// once reports the second use of a term that UBL or EN 16931 gives only one
// element to. The render completes but fails, because silently picking one of
// two different values is worse than saying so.
func (x *xw) once(seen map[string]bool, key, what string) bool {
	if seen[key] {
		x.badf("%s appears more than once: %s", what, key)
		return false
	}
	seen[key] = true
	return true
}

// boolText writes a UBL indicator, which is a plain xsd:boolean.
func boolText(v bool) string {
	if v {
		return "true"
	}
	return "false"
}

// RenderUBL serialises inv as an OASIS UBL Invoice 2.1 document. It fills in
// whatever the model left to be calculated, then writes the document in schema
// order, collecting every term it cannot place.
func RenderUBL(inv Invoice) ([]byte, error) {
	cp := prepare(inv)
	if err := cp.Derive(); err != nil {
		return nil, err
	}
	// A credit note type code belongs in a CreditNote document: the same
	// code is rejected inside an Invoice by BR-CL-01.
	cn := isCreditType(cp.TypeCode)
	root, ns := "Invoice", NSUBL
	if cn {
		root, ns = "CreditNote", NSCREDIT
	}
	x := &xw{}
	seen := map[string]bool{}
	x.start(root,
		attr{"xmlns", ns},
		attr{"xmlns:cac", NSCAC},
		attr{"xmlns:cbc", NSCBC})
	ublHead(x, &cp, seen, cn)
	ublParties(x, &cp, seen)
	ublDelivery(x, cp.Delivery, seen)
	ublSettlement(x, &cp, seen)
	for _, ln := range cp.Lines {
		ublLine(x, &cp, ln, cn)
	}
	x.end(root)
	if len(x.errs) > 0 {
		return nil, x.errf("ubl")
	}
	return []byte(x.doc()), nil
}

// ublHead writes the document identifiers, dates, notes, currency and
// references that come before the parties.
func ublHead(x *xw, in *Invoice, seen map[string]bool, cn bool) {
	g := in.Guideline
	if g == "" {
		g = UBLProfile
	}
	x.opt("cbc:CustomizationID", g)
	x.opt("cbc:ProfileID", in.ProcessType)
	x.mand("cbc:ID", in.ID)
	x.mand("cbc:IssueDate", isoDate(in.IssueDate))
	if cn {
		// A UBL credit note has no due date element, so a due date on the
		// model invoice cannot be carried across.
		if in.DueDate != "" {
			x.badf("a UBL credit note has no due date: %q", in.DueDate)
		}
		x.mand("cbc:CreditNoteTypeCode", in.TypeCode)
	} else {
		x.ublDate("cbc:DueDate", in.DueDate)
		x.mand("cbc:InvoiceTypeCode", in.TypeCode)
	}
	for _, n := range in.Notes {
		x.opt("cbc:Note", n)
	}
	x.mand("cbc:DocumentCurrencyCode", in.Currency)
	if in.Accounting != nil {
		// cbc:AccountingCostCode would trip UBL-CR-010, so the posting
		// reference travels as free text only.
		x.opt("cbc:AccountingCost", in.Accounting.ID)
	}
	x.opt("cbc:BuyerReference", in.BuyerReference)
	if in.Period != nil {
		// As in CII, the period narrative is no business term, so it has no element.
		ublPeriod(x, "cac:InvoicePeriod", *in.Period)
	}
	ublRefs(x, in.References, seen, cn)
}

// refSlots holds at most one reference per UBL reference element. The order
// references share one element, so they are kept together.
type refSlots struct {
	contract, project, despatch, receipt, quotation, preceding *Reference
	order                                                      [3]*Reference
}

// orderSlots maps each order reference the model knows to the slot it wants in
// cac:OrderReference, and says what to call it when a second one turns up.
var orderSlots = map[string]struct {
	slot int
	what string
}{
	"buyerOrder": {0, "buyer order"},
	"purchase":   {1, "purchase order"},
	"salesOrder": {2, "sales order"},
}

// refSlotsFor maps the remaining reference types to the slot they fill.
var refSlotsFor = map[string]func(*refSlots, *Reference){
	"contract":     func(s *refSlots, r *Reference) { s.contract = r },
	"project":      func(s *refSlots, r *Reference) { s.project = r },
	"despatch":     func(s *refSlots, r *Reference) { s.despatch = r },
	"deliveryNote": func(s *refSlots, r *Reference) { s.receipt = r },
	"quotation":    func(s *refSlots, r *Reference) { s.quotation = r },
	"preceding":    func(s *refSlots, r *Reference) { s.preceding = r },
}

// ublRefs routes the model's references into the reference elements UBL
// offers, in schema order. A reference with no element of its own is reported
// rather than parked in a reference of a different kind.
func ublRefs(x *xw, refs []Reference, seen map[string]bool, cn bool) {
	s := &refSlots{}
	for i := range refs {
		r := refs[i]
		if at, ok := orderSlots[r.Type]; ok {
			s.order[at.slot] = pick(x, s.order[at.slot], &r, at.what)
			continue
		}
		if put, ok := refSlotsFor[r.Type]; ok {
			put(s, &r)
			continue
		}
		x.badf("reference type %q has no UBL element", r.Type)
	}
	ublOrderReference(x, s)
	ublDocumentReferences(x, seen, s, cn)
}

// ublOrderReference writes cac:OrderReference. UBL gives the order references
// one element with two slots, and the schema makes cbc:ID mandatory inside it,
// so at most two of the three order references the model knows fit on an
// invoice. Where both a buyer order reference (BT-10) and a purchase order
// reference (BT-13) are present they are paired the way Peppol BIS pairs them:
// the buyer order reference takes cbc:ID, the purchase order reference
// cbc:SalesOrderID.
func ublOrderReference(x *xw, s *refSlots) {
	buyer, purchase, sales := s.order[0], s.order[1], s.order[2]
	var slots []*Reference
	if buyer != nil && purchase != nil {
		// The Peppol pairing: buyer order in the ID slot leaves the
		// sales order reference with nowhere to go.
		slots = []*Reference{buyer, purchase}
		if sales != nil {
			x.badf("only two order references fit a UBL invoice: %q", sales.ID)
		}
	}
	for _, r := range []*Reference{purchase, buyer, sales} {
		if r != nil && len(slots) < 2 {
			slots = append(slots, r)
		}
	}
	if len(slots) == 0 {
		return
	}
	x.start("cac:OrderReference")
	x.mand("cbc:ID", slots[0].ID)
	if len(slots) > 1 {
		x.opt("cbc:SalesOrderID", slots[1].ID)
	}
	x.end("cac:OrderReference")
}

// ublDocumentReferences writes the remaining reference elements, in the order
// the schema lists them.
func ublDocumentReferences(x *xw, seen map[string]bool, s *refSlots, cn bool) {
	if s.preceding != nil {
		if x.once(seen, "billing", "preceding invoice reference") {
			x.start("cac:BillingReference")
			ublDocRef(x, "cac:InvoiceDocumentReference", s.preceding.ID)
			x.end("cac:BillingReference")
		}
	}
	if s.despatch != nil {
		ublDocRef(x, "cac:DespatchDocumentReference", s.despatch.ID)
	}
	if s.receipt != nil {
		ublDocRef(x, "cac:ReceiptDocumentReference", s.receipt.ID)
	}
	if s.contract != nil {
		ublDocRef(x, "cac:ContractDocumentReference", s.contract.ID)
	}
	if s.quotation != nil {
		// A supporting document may not carry a scheme identifier unless it is
		// typed as an invoiced object (UBL-SR-43), so only the number goes in.
		ublDocRef(x, "cac:AdditionalDocumentReference", s.quotation.ID)
	}
	ublProjectReference(x, seen, s, cn)
}

// ublProjectReference writes cac:ProjectReference, which carries its identifier
// alone (BT-11): UBL-CR-134 to UBL-CR-136 warn away the rest of
// ProjectReference, and a project name is not a business term of the standard.
// The credit note document has no project reference element at all.
func ublProjectReference(x *xw, seen map[string]bool, s *refSlots, cn bool) {
	if s.project == nil {
		return
	}
	if cn {
		x.badf("a UBL credit note has no project reference: %q", s.project.ID)
		return
	}
	if x.once(seen, "project", "project reference") {
		ublDocRef(x, "cac:ProjectReference", s.project.ID)
	}
}

// pick keeps the first reference of a kind and reports the rest, which the
// single UBL element for that kind could not hold anyway.
func pick(x *xw, had, next *Reference, what string) *Reference {
	if had != nil {
		x.badf("only one %s reference fits UBL: %q", what, next.ID)
		return had
	}
	return next
}

// ublDocRef writes a document reference reduced to its identifier, the shape
// every EN 16931 reference except the preceding invoice takes in UBL.
func ublDocRef(x *xw, name, id string) {
	x.start(name)
	x.mand("cbc:ID", id)
	x.end(name)
}

// ublPeriod writes an invoicing or delivery period.
func ublPeriod(x *xw, name string, p Period) {
	if p.Start == "" && p.End == "" {
		return
	}
	x.start(name)
	x.ublDate("cbc:StartDate", p.Start)
	x.ublDate("cbc:EndDate", p.End)
	x.end(name)
}

// ublParties writes the seller, the buyer and, when there is one, the payee.
// Buyer customer party, seller supplier party and tax representative party are
// UBL alternatives that EN 16931 does not use.
func ublParties(x *xw, in *Invoice, seen map[string]bool) {
	x.start("cac:AccountingSupplierParty")
	x.start("cac:Party")
	ublParty(x, in.Seller.Party, in.Seller.TradingName, false, seen)
	x.end("cac:Party")
	x.end("cac:AccountingSupplierParty")

	x.start("cac:AccountingCustomerParty")
	x.start("cac:Party")
	ublParty(x, in.Buyer, "", false, seen)
	x.end("cac:Party")
	x.end("cac:AccountingCustomerParty")

	if in.Payee != nil {
		x.start("cac:PayeeParty")
		ublParty(x, *in.Payee, "", true, seen)
		x.end("cac:PayeeParty")
	}
}

// ublParty writes a party. payee selects the payee shape, which the schematron
// keeps short: UBL-CR-265 to 293 warn away its address, contact, endpoint and
// tax registration, and a payee has no registration name.
func ublParty(x *xw, p Party, tradingName string, payee bool, seen map[string]bool) {
	if !payee && p.EAddress != "" {
		x.elem("cbc:EndpointID", p.EAddress, attr{"schemeID", p.EASCode()})
	}
	if p.Identifier != "" {
		leaf(x, "cac:PartyIdentification", "cbc:ID", p.Identifier, attr{"schemeID", p.IDScheme})
	}
	ublPartyName(x, p, tradingName, payee, seen)
	if !payee {
		ublAddress(x, "cac:PostalAddress", p.Address, p.Subdiv)
	}
	for _, t := range ublTaxRegistrations(x, p) {
		// UBL-SR-53: a tax registration is accepted only together with the
		// tax scheme element, and that scheme may carry nothing but its
		// identifier. The identifier itself goes unlabelled: UBL-CR-652 warns
		// away a scheme identifier on it, and the scheme around it already
		// names the register the number came from.
		x.start("cac:PartyTaxScheme")
		x.elem("cbc:CompanyID", t.id.Value)
		x.ublTaxSchemeKind(t.scheme)
		x.end("cac:PartyTaxScheme")
	}
	ublLegalEntity(x, p, payee)
	ublContact(x, p, payee)
}

// ublPartyName writes cac:PartyName. A trading name (BT-28 and its neighbours)
// is the name of the branch, so it wins over the party's own name; the payee's
// name (BT-59) has to be carried somewhere, and
// PartyLegalEntity/RegistrationName is warned away for a payee.
func ublPartyName(x *xw, p Party, tradingName string, payee bool, seen map[string]bool) {
	if tradingName != "" {
		leaf(x, "cac:PartyName", "cbc:Name", tradingName)
		return
	}
	if payee && p.Name != "" && x.once(seen, "payeename", "payee name") {
		leaf(x, "cac:PartyName", "cbc:Name", p.Name)
	}
}

// ublTaxReg is one registration number and the tax scheme naming the register
// that issued it.
type ublTaxReg struct {
	id     TaxID
	scheme string
}

// ublTaxRegistrations returns the tax registration numbers a party has, which
// UBL writes into cac:PartyTaxScheme: BT-31 for the seller and BT-48 for the
// buyer under the VAT scheme, BT-32 under the TAX scheme. CII labels each
// number with its register instead (VA is the VAT number, FC the fiscal number
// a German profile uses in its place), and UBL's rules read the scheme, not a
// label: BR-CO-09 wants an ISO country prefix in a VAT-schemed identifier,
// BR-O-02 and BR-O-04 forbid a VAT-schemed identifier outright whenever
// anything is category O, and UBL-SR-12 allows only one of them. A fiscal
// number written under VAT is therefore taken for a VAT number by everything
// that checks those rules, so it goes into the TAX scheme instead. UBL-SR-42
// gives a party two tax scheme elements in total, so a number beyond those two
// has nowhere to go and the render says so rather than choosing one.
func ublTaxRegistrations(x *xw, p Party) []ublTaxReg {
	vat, fiscal := []TaxID{}, []TaxID{}
	for _, t := range p.TaxIDs {
		if t.Value == "" {
			continue
		}
		if t.Scheme == "" || t.Scheme == "VA" || t.Scheme == "VAT" {
			vat = append(vat, t)
			continue
		}
		fiscal = append(fiscal, t)
	}
	out := []ublTaxReg{}
	for _, t := range vat {
		if len(out) > 0 {
			x.badf("a UBL party has room for one VAT registration, not %q", t.Value)
			continue
		}
		out = append(out, ublTaxReg{id: t, scheme: ublTaxSchemeID})
	}
	if len(fiscal) > 0 {
		if len(fiscal) > 1 {
			x.badf("a UBL party has room for one tax registration, not %q", fiscal[1:])
		}
		out = append(out, ublTaxReg{id: fiscal[0], scheme: ublFiscalSchemeID})
	}
	return out
}

// ublLegalEntity writes cac:PartyLegalEntity, whose company identifier is the
// legal registration identifier (BT-30 for the seller, BT-47 for the buyer). A
// tax registration number never belongs here: it describes a tax register, not
// a company register, and UBL gives it its own element. A payee has no
// registration name, so its element exists only to carry the identifier.
func ublLegalEntity(x *xw, p Party, payee bool) {
	reg := ""
	if len(p.RegIDs) > 0 {
		reg = p.RegIDs[0]
	}
	if len(p.RegIDs) > 1 {
		x.badf("only one legal registration identifier fits a UBL party: %q", p.RegIDs[1:])
	}
	if payee && reg == "" {
		return
	}
	if !payee && p.Name == "" {
		return
	}
	x.start("cac:PartyLegalEntity")
	if !payee {
		x.mand("cbc:RegistrationName", p.Name)
	}
	x.opt("cbc:CompanyID", reg, attr{"schemeID", p.RegScheme})
	x.end("cac:PartyLegalEntity")
}

// ublContact writes cac:Contact, which the schematron warns away for a payee.
func ublContact(x *xw, p Party, payee bool) {
	c := p.Contact
	if payee || c == nil || (c.Name == "" && c.Phone == "" && c.Email == "") {
		return
	}
	x.start("cac:Contact")
	x.opt("cbc:Name", c.Name)
	x.opt("cbc:Telephone", c.Phone)
	x.opt("cbc:ElectronicMail", c.Email)
	x.end("cac:Contact")
}

// ublAddress writes a postal address. The third model line goes to
// cac:AddressLine, of which UBL-SR-51 allows only one per address. The post
// name has no UBL home: UBL-CR-358 and its neighbours warn away every part of
// an address that EN 16931 does not use, the post box among them.
func ublAddress(x *xw, name string, a Address, subdiv string) {
	x.start(name)
	x.opt("cbc:StreetName", a.Line1)
	x.opt("cbc:AdditionalStreetName", a.Line2)
	x.opt("cbc:CityName", a.City)
	x.opt("cbc:PostalZone", a.Postal)
	zone := a.Zone
	if zone == "" {
		zone = subdiv
	}
	x.opt("cbc:CountrySubentity", zone)
	// The address line comes after the subentity and before the country, which
	// is where the UBL schema puts it; writing it in the order the model lists
	// its lines produces a document the schema refuses.
	if a.Line3 != "" {
		leaf(x, "cac:AddressLine", "cbc:Line", a.Line3)
	}
	if a.Country != "" {
		x.start("cac:Country")
		x.mand("cbc:IdentificationCode", a.Country)
		x.end("cac:Country")
	}
	x.end(name)
}

// ublDelivery writes the delivery of goods. EN 16931 allows one delivery
// (UBL-SR-24), and its address belongs inside the delivery location:
// UBL-CR-682 warns away cac:Delivery/cac:DeliveryAddress.
func ublDelivery(x *xw, d *Delivery, seen map[string]bool) {
	if d == nil {
		return
	}
	if !x.once(seen, "delivery", "delivery information") {
		return
	}
	x.start("cac:Delivery")
	x.ublDate("cbc:ActualDeliveryDate", d.ArrivalDate)
	if d.ReceiverID != "" || !emptyAddress(d.Address) {
		x.start("cac:DeliveryLocation")
		x.opt("cbc:ID", d.ReceiverID)
		ublAddress(x, "cac:Address", d.Address, "")
		x.end("cac:DeliveryLocation")
	}
	if d.Period != nil {
		ublPeriod(x, "cac:PromisedDeliveryPeriod", *d.Period)
	}
	if d.Name != "" {
		// The deliver to party name (BT-70) belongs to the delivery party:
		// UBL-CR-357 warns away cac:Delivery/cac:DeliveryLocation/cbc:Name.
		x.start("cac:DeliveryParty")
		leaf(x, "cac:PartyName", "cbc:Name", d.Name)
		x.end("cac:DeliveryParty")
	}
	// The despatch date and the incoterms have no UBL home: UBL-CR-396 and
	// UBL-CR-410 warn away the delivery terms element, and UBL-CR-404 the
	// despatch element that would otherwise hold the despatch date.
	x.end("cac:Delivery")
}

// ublSettlement writes the payment instructions, the document level
// corrections, the tax breakdown and the monetary totals, in schema order.
func ublSettlement(x *xw, in *Invoice, seen map[string]bool) {
	cur := in.Currency
	if p := in.Payment; p != nil {
		ublPaymentMeans(x, in, p, seen)
	}
	if len(in.Terms) > 0 {
		x.start("cac:PaymentTerms")
		// UBL-SR-05 allows one payment terms note, so the model's terms are
		// joined into it rather than dropped or split over several elements.
		x.mand("cbc:Note", joinLines(in.Terms))
		x.end("cac:PaymentTerms")
	}
	line, _ := lineTotalBasis(in).Float64()
	for _, c := range in.Charges {
		ublAC(x, c, true, line, cur)
	}
	allowanceBasis := line
	if in.Totals.ChargeTotal != nil {
		allowanceBasis += *in.Totals.ChargeTotal
	}
	for _, a := range in.Allowances {
		ublAC(x, a, false, allowanceBasis, cur)
	}
	ublVAT(x, in, cur, seen)
	x.start("cac:LegalMonetaryTotal")
	x.amt("cbc:LineExtensionAmount", cur, line)
	x.pamt("cbc:TaxExclusiveAmount", cur, in.Totals.TaxExclusive)
	x.pamt("cbc:TaxInclusiveAmount", cur, in.Totals.TaxInclusive)
	x.oamt("cbc:AllowanceTotalAmount", cur, in.Totals.AllowanceTotal)
	x.oamt("cbc:ChargeTotalAmount", cur, in.Totals.ChargeTotal)
	x.oamt("cbc:PrepaidAmount", cur, in.Prepaid)
	x.oamt("cbc:PayableRoundingAmount", cur, in.Rounding)
	x.pamt("cbc:PayableAmount", cur, in.Totals.Payable)
	x.end("cac:LegalMonetaryTotal")
}

// ublPaymentMeans writes the payment instructions. The model carries one
// payment block, which suits both syntaxes: UBL-SR-47 requires the code of a
// second payment means element to be the same as the first anyway.
func ublPaymentMeans(x *xw, in *Invoice, p *Payment, seen map[string]bool) {
	x.start("cac:PaymentMeans")
	if p.Code != "" {
		x.elem("cbc:PaymentMeansCode", p.Code, attr{"name", p.Name})
	}
	if p.RemitInfo != "" {
		// cbc:InstructionID is warned away by UBL-CR-414, and the remittance
		// identifier is BT-83 in any case.
		if x.once(seen, "paymentid", "payment reference") {
			x.elem("cbc:PaymentID", p.RemitInfo)
		}
	}
	if p.Card != nil {
		if x.once(seen, "card", "payment card") {
			x.start("cac:CardAccount")
			x.mand("cbc:PrimaryAccountNumberID", p.Card.Pan)
			network := p.Card.Scheme
			if network == "" {
				network = "CARD"
			}
			x.mand("cbc:NetworkID", network)
			// cbc:CardTypeCode is warned away by UBL-CR-415, so the card type
			// has no UBL home.
			x.opt("cbc:HolderName", p.Card.Holder)
			x.end("cac:CardAccount")
		}
	}
	if in.SellerAccount != nil {
		x.start("cac:PayeeFinancialAccount")
		ublAccount(x, *in.SellerAccount, true)
		x.end("cac:PayeeFinancialAccount")
	}
	if m := p.Mandate; m != nil {
		if x.once(seen, "mandate", "payment mandate") {
			x.start("cac:PaymentMandate")
			x.mand("cbc:ID", m.ID)
			if m.Bank != nil {
				// UBL-CR-440 warns away a name on the payer account of a
				// mandate, so the account is written without one.
				x.start("cac:PayerFinancialAccount")
				ublAccount(x, *m.Bank, false)
				x.end("cac:PayerFinancialAccount")
			}
			x.end("cac:PaymentMandate")
		}
	}
	// A buyer account without a mandate has no UBL home: UBL-CR-680 warns away
	// cac:PaymentMeans/cac:PayerFinancialAccount.
	x.end("cac:PaymentMeans")
}

// ublAccount writes a bank account. EN 16931 requires an account identifier,
// so the IBAN wins over a private account number when both are present.
func ublAccount(x *xw, a FinancialAcct, name bool) {
	id := a.IBAN
	if id == "" {
		id = a.OtherID
	}
	if a.IBAN != "" && a.OtherID != "" {
		x.badf("a UBL bank account holds one identifier, so the private number %q is dropped", a.OtherID)
	}
	if id == "" {
		x.bad("a bank account has no identifier")
	} else {
		x.elem("cbc:ID", id)
	}
	if name {
		x.opt("cbc:Name", a.Name)
	}
	if a.BIC != "" {
		// UBL-SR-58 wants exactly one identifier in the branch element.
		x.start("cac:FinancialInstitutionBranch")
		x.mand("cbc:ID", a.BIC)
		x.end("cac:FinancialInstitutionBranch")
	}
}

// ublAC writes a document level allowance or charge. UBL-CR-471 to 476 warn
// away the identifier, the sequence, the per unit amount and the accounting
// reference of the element, and an exempt reason on a correction has no UBL
// home at all (UBL-CR-480, UBL-CR-481).
func (x *xw) acBody(a AllowanceCharge, charge bool, basis float64, cur string, cat string, rate *float64, tax bool) {
	x.elem("cbc:ChargeIndicator", boolText(charge))
	x.opt("cbc:AllowanceChargeReasonCode", a.ReasonCode)
	x.opt("cbc:AllowanceChargeReason", a.Reason)
	if a.Percent != nil {
		// The multiplier is the percentage itself (BT-94 for an allowance,
		// BT-101 for a charge): UBL divides by 100 when it applies it, as
		// PEPPOL-EN16931-R040 does.
		x.elem("cbc:MultiplierFactorNumeric", Rate(*a.Percent))
	}
	// The schema puts the amount before the amount it was taken from, so a
	// percentage and its basis cannot be written as a pair of neighbours.
	x.amt("cbc:Amount", cur, acAmount(a, basis))
	if a.Percent != nil || a.Basis != nil {
		x.amt("cbc:BaseAmount", cur, acBasisOf(a, basis))
	}
	if !tax {
		return
	}
	// Only a document level allowance or charge carries VAT of its own
	// (BT-95, BT-96); a line level one takes the VAT of its line, and
	// UBL-CR-558 warns away a tax category there.
	if cat == "" {
		cat = "S"
	}
	x.start("cac:TaxCategory")
	x.mand("cbc:ID", cat)
	if rate != nil && cat != "O" {
		x.elem("cbc:Percent", Rate(*rate))
	}
	x.ublTaxScheme()
	x.end("cac:TaxCategory")
}

func ublAC(x *xw, a AllowanceCharge, charge bool, basis float64, cur string) {
	x.start("cac:AllowanceCharge")
	x.acBody(a, charge, basis, cur, a.TaxCategory, a.TaxRate, true)
	x.end("cac:AllowanceCharge")
}

// ublVAT writes the tax total. UBL puts one total on an invoice and repeats a
// subtotal for every rate.
func ublVAT(x *xw, in *Invoice, cur string, seen map[string]bool) {
	x.start("cac:TaxTotal")
	x.pamt("cbc:TaxAmount", cur, in.Totals.TaxTotal)
	for _, b := range in.VATBreakdown {
		x.start("cac:TaxSubtotal")
		x.amt("cbc:TaxableAmount", cur, b.Taxable)
		x.amt("cbc:TaxAmount", cur, b.Amount)
		// UBL 2.1 has no exemption reason on the subtotal itself, and
		// UBL-CR-499 warns away a percent there either, so both go into the
		// category, as the Peppol mapping does.
		x.start("cac:TaxCategory")
		x.mand("cbc:ID", b.Category)
		// The rate belongs in the category whatever the category is. BR-48
		// excuses it only for a document with no VAT at all, and the German
		// rules ask for BT-119 under category O too (BR-DE-14). UBL-CR-499
		// warns away a percent on the subtotal itself, which is why it sits
		// here next to the exemption reason.
		x.elem("cbc:Percent", Rate(b.Percent))
		if e := b.Exempt; e != nil {
			x.opt("cbc:TaxExemptionReasonCode", e.ReasonCode)
			if e.Reason != "" {
				if x.once(seen, "exemptreason", "VAT exemption reason") {
					x.elem("cbc:TaxExemptionReason", e.Reason)
				}
			}
		}
		x.ublTaxScheme()
		x.end("cac:TaxCategory")
		x.end("cac:TaxSubtotal")
	}
	x.end("cac:TaxTotal")
}

// ublLine writes one invoice line: identifier, quantity, amount, period,
// corrections, item and price.
func ublLine(x *xw, in *Invoice, ln Line, cn bool) {
	cur := in.Currency
	base, _ := lineBase(ln).Float64()
	elem, qname := "cac:InvoiceLine", "cbc:InvoicedQuantity"
	if cn {
		elem, qname = "cac:CreditNoteLine", "cbc:CreditedQuantity"
	}
	x.start(elem)
	x.mand("cbc:ID", ln.ID)
	if ln.Note != "" {
		x.elem("cbc:Note", ln.Note)
	}
	x.qty(qname, ln.Quantity, ln.UnitCode)
	x.pamt("cbc:LineExtensionAmount", cur, ln.Extension)
	if ln.Account != nil {
		x.opt("cbc:AccountingCost", ln.Account.ID)
	}
	if ln.Period != nil {
		ublPeriod(x, "cac:InvoicePeriod", *ln.Period)
	}
	for _, a := range ln.Allowances {
		x.start("cac:AllowanceCharge")
		x.acBody(a, false, base, cur, "", nil, false)
		x.end("cac:AllowanceCharge")
	}
	for _, c := range ln.Charges {
		x.start("cac:AllowanceCharge")
		x.acBody(c, true, base, cur, "", nil, false)
		x.end("cac:AllowanceCharge")
	}
	ublItem(x, ln)
	ublPrice(x, ln, cur)
	x.end(elem)
}

// ublItem writes the item of a line. The order of the identifiers matters:
// the buyer's, then the seller's, then the standard one.
func ublItem(x *xw, ln Line) {
	it := ln.Item
	cat := catOfLine(ln)
	x.start("cac:Item")
	x.opt("cbc:Description", it.Description)
	x.mand("cbc:Name", it.Name)
	leaf(x, "cac:BuyersItemIdentification", "cbc:ID", it.CustomerID)
	leaf(x, "cac:SellersItemIdentification", "cbc:ID", it.SellerID)
	leaf(x, "cac:StandardItemIdentification", "cbc:ID", it.StandardID, attr{"schemeID", it.StdScheme})
	if it.Origin != "" {
		x.start("cac:OriginCountry")
		x.mand("cbc:IdentificationCode", it.Origin)
		x.end("cac:OriginCountry")
	}
	for _, c := range it.ClassIDs {
		// BR-65 wants a list identifier on every classification code.
		x.start("cac:CommodityClassification")
		x.mand("cbc:ItemClassificationCode", c, attr{"listID", it.ClassScheme})
		x.end("cac:CommodityClassification")
	}
	// UBL-SR-48: exactly one classified tax category per line.
	x.start("cac:ClassifiedTaxCategory")
	x.mand("cbc:ID", cat)
	if cat != "O" {
		x.elem("cbc:Percent", Rate(ln.TaxRate))
	}
	x.ublTaxScheme()
	x.end("cac:ClassifiedTaxCategory")
	for _, at := range it.Attributes {
		ublAttr(x, at)
	}
	for _, at := range ln.Attributes {
		ublAttr(x, at)
	}
	x.end("cac:Item")
}

// ublAttr writes an item attribute (BG-32, BT-160s). BR-54 asks for both a
// name and a value, and UBL takes nothing else in the element.
func ublAttr(x *xw, a Attribute) {
	x.start("cac:AdditionalItemProperty")
	x.mand("cbc:Name", a.Name)
	x.mand("cbc:Value", a.Value)
	x.end("cac:AdditionalItemProperty")
}

// ublPrice writes the price of a line. UBL has no gross price element, so the
// price discount (BG-29) carries the difference: the net price is the price
// amount, and the discount is stated against the gross price. The base
// quantity is what the price is quoted for, so it defaults to one unit.
func ublPrice(x *xw, ln Line, cur string) {
	q, u := 1.0, ln.UnitCode
	if p := ln.Item.Price; p != nil {
		if p.BaseQty != 0 {
			q = p.BaseQty
		}
		if p.BaseUnit != "" {
			u = p.BaseUnit
		}
	}
	net, gross, disc, bad := priceParts(ln)
	if bad != "" {
		x.bad(bad)
	}
	x.start("cac:Price")
	x.amt("cbc:PriceAmount", cur, net)
	x.qty("cbc:BaseQuantity", q, u)
	if disc != 0 {
		// UBL-SR-37 allows one item price discount, and UBL-CR-633, 634,
		// 635 and 641 warn away its reason, its percentage and its tax
		// information, so the element carries the discount and the gross
		// price it was taken off and nothing else.
		x.start("cac:AllowanceCharge")
		x.elem("cbc:ChargeIndicator", boolText(false))
		x.amt("cbc:Amount", cur, disc)
		x.amt("cbc:BaseAmount", cur, gross)
		x.end("cac:AllowanceCharge")
	}
	x.end("cac:Price")
}

// joinLines joins the lines of free text into the single element a syntax
// gives them.
func joinLines(lines []string) string {
	var out string
	for _, l := range lines {
		if l == "" {
			continue
		}
		if out != "" {
			out += "\n"
		}
		out += l
	}
	return out
}
