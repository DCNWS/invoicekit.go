package model

import "strings"

// The CII (UN/CEFACT Cross Industry Invoice, D16B) namespaces. This is the XML
// that Factur-X, ZUGFeRD and XRechnung carry, either beside the PDF or inside it.
const (
	NSRSM = "urn:un:unece:uncefact:data:standard:CrossIndustryInvoice:100"
	NSRAM = "urn:un:unece:uncefact:data:standard:ReusableAggregateBusinessInformationEntity:100"
	NSUDT = "urn:un:unece:uncefact:data:standard:UnqualifiedDataType:100"

	// CIIProfile is the BT-24 guideline identifier a plain EN 16931 CII
	// invoice declares (BR-01 requires it to be present and non-empty).
	CIIProfile = "urn:cen.eu:en16931:2017"
)

// RenderCII renders an invoice as CII.
//
// The D16B schema is a chain of xs:sequence, so the set of children and their
// order are both part of validity; every element below is emitted in the order
// the schema declares it. Terms that EN 16931 makes mandatory are emitted even
// when the model leaves them empty, which keeps the document well formed and
// schematically valid, and lets the validator name the term that is missing
// instead of the renderer refusing to produce anything. Only a structure with
// no CII element at all makes rendering fail.
//
// The CII schematron carries a long list of advisory rules, each one saying
// that some CII element "should not be present". They are warnings, not
// failures, and the official validator reports them while still passing the
// document. Almost all of them exist because CII can say more than EN 16931
// asks for, and they fire on data, not on mistakes: incoterms (CII-SR-293), a
// quotation reference (CII-SR-296), a despatch date or a delivery period
// beside the actual delivery date (CII-SR-319, CII-SR-329), a despatch or
// delivery note reference (CII-SR-335), a postal address on the payee
// (CII-SR-360), a card type code (CII-SR-373), a name on the paying account
// (CII-SR-382), a type on the buyer's accounting account (CII-SR-429). Where
// a caller supplies such a value the renderer writes it, because the
// alternative is to throw the customer's data away and CII is the only syntax
// that has a place for it. The advisories our own output attracts are pinned
// in gen_test.go, so taking on a new one is a decision, not a surprise.
func RenderCII(inv Invoice) ([]byte, error) {
	cp := prepare(inv)
	if err := cp.Derive(); err != nil {
		return nil, err
	}

	x := &xw{}
	x.start("rsm:CrossIndustryInvoice",
		attr{"xmlns:rsm", NSRSM},
		attr{"xmlns:ram", NSRAM},
		attr{"xmlns:udt", NSUDT})
	ciiContext(x, cp)
	ciiDocument(x, cp)

	x.start("rsm:SupplyChainTradeTransaction")
	for _, ln := range cp.Lines {
		ciiLine(x, ln)
	}
	ciiAgreement(x, cp)
	ciiDelivery(x, cp)
	ciiSettlement(x, cp)
	x.end("rsm:SupplyChainTradeTransaction")

	x.end("rsm:CrossIndustryInvoice")
	if len(x.errs) > 0 {
		return nil, x.errf("cii")
	}
	return []byte(x.doc()), nil
}

// prepare copies the slices Derive fills in, so rendering never writes through
// into the invoice it was handed.
func prepare(inv Invoice) Invoice {
	cp := inv
	cp.Lines = append([]Line(nil), inv.Lines...)
	cp.Allowances = append([]AllowanceCharge(nil), inv.Allowances...)
	cp.Charges = append([]AllowanceCharge(nil), inv.Charges...)
	cp.VATBreakdown = append([]VATRate(nil), inv.VATBreakdown...)
	return cp
}

// dt writes a CII date: the schema wants a wrapper holding a udt:DateTimeString
// with an explicit format code (BR-02/BT-2 is checked on that inner element).
func (x *xw) dt(name, v string) {
	s := isoDate(v)
	x.start(name)
	if len(s) == 10 && s[4] == '-' && s[7] == '-' {
		x.elem("udt:DateTimeString", strings.ReplaceAll(s, "-", ""), attr{"format", "102"})
	} else {
		x.mand("udt:DateTimeString", "", attr{"format", "102"})
	}
	x.end(name)
}

// omoney writes an optional amount: absent means the element is not there at
// all, which is how a paid or rounded amount that was never supplied reads.
func (x *xw) omoney(name string, v *float64) {
	if v == nil {
		return
	}
	x.money(name, *v)
}

func (x *xw) indicator(v bool) {
	s := "false"
	if v {
		s = "true"
	}
	x.start("ram:ChargeIndicator")
	x.elem("udt:Indicator", s)
	x.end("ram:ChargeIndicator")
}

func ciiContext(x *xw, in Invoice) {
	x.start("rsm:ExchangedDocumentContext")
	if in.ProcessType != "" {
		x.start("ram:BusinessProcessSpecifiedDocumentContextParameter")
		x.mand("ram:ID", in.ProcessType)
		x.end("ram:BusinessProcessSpecifiedDocumentContextParameter")
	}
	g := in.Guideline
	if g == "" {
		g = CIIProfile
	}
	x.start("ram:GuidelineSpecifiedDocumentContextParameter")
	x.mand("ram:ID", g)
	x.end("ram:GuidelineSpecifiedDocumentContextParameter")
	x.end("rsm:ExchangedDocumentContext")
}

func ciiDocument(x *xw, in Invoice) {
	x.start("rsm:ExchangedDocument")
	x.mand("ram:ID", in.ID)
	x.opt("ram:TypeCode", in.TypeCode)
	x.dt("ram:IssueDateTime", in.IssueDate)
	for _, n := range in.Notes {
		x.start("ram:IncludedNote")
		x.mand("ram:Content", n)
		x.end("ram:IncludedNote")
	}
	x.end("rsm:ExchangedDocument")
}

func ciiLine(x *xw, ln Line) {
	cat := catOfLine(ln)

	x.start("ram:IncludedSupplyChainTradeLineItem")
	x.start("ram:AssociatedDocumentLineDocument")
	x.mand("ram:LineID", ln.ID)
	if ln.Note != "" {
		x.start("ram:IncludedNote")
		x.mand("ram:Content", ln.Note)
		x.end("ram:IncludedNote")
	}
	x.end("ram:AssociatedDocumentLineDocument")

	ciiProduct(x, ln)

	x.start("ram:SpecifiedLineTradeAgreement")
	base, _ := lineBase(ln).Float64()
	net, gross, disc, bad := priceParts(ln)
	if bad != "" {
		x.bad(bad)
	}
	if gross != net || disc != 0 {
		x.start("ram:GrossPriceProductTradePrice")
		x.money("ram:ChargeAmount", gross)
		ciiPriceBasis(x, ln)
		if disc != 0 {
			// The price discount belongs to the gross price and is written
			// as an AppliedTradeAllowanceCharge. The document level
			// SpecifiedTradeAllowanceCharge is not allowed here, and the
			// applied one carries nothing but the flag and the amount:
			// CII-SR-119 wants the pair, CII-SR-122 to CII-SR-130 warn away
			// a percentage, a basis, a reason and a tax block.
			x.start("ram:AppliedTradeAllowanceCharge")
			x.indicator(false)
			x.money("ram:ActualAmount", disc)
			x.end("ram:AppliedTradeAllowanceCharge")
		}
		x.end("ram:GrossPriceProductTradePrice")
	}
	x.start("ram:NetPriceProductTradePrice")
	x.money("ram:ChargeAmount", net)
	ciiPriceBasis(x, ln)
	x.end("ram:NetPriceProductTradePrice")
	x.end("ram:SpecifiedLineTradeAgreement")

	x.start("ram:SpecifiedLineTradeDelivery")
	x.elem("ram:BilledQuantity", Rate(ln.Quantity), attr{"unitCode", ln.UnitCode})
	x.end("ram:SpecifiedLineTradeDelivery")

	x.start("ram:SpecifiedLineTradeSettlement")
	x.start("ram:ApplicableTradeTax")
	x.elem("ram:TypeCode", "VAT")
	if ln.TaxExempt != nil {
		x.opt("ram:ExemptionReason", ln.TaxExempt.Reason)
	}
	x.mand("ram:CategoryCode", cat)
	if ln.TaxExempt != nil {
		x.opt("ram:ExemptionReasonCode", ln.TaxExempt.ReasonCode)
	}
	if cat != "O" {
		x.elem("ram:RateApplicablePercent", Rate(ln.TaxRate))
	}
	x.end("ram:ApplicableTradeTax")
	if ln.Period != nil {
		ciiPeriod(x, "ram:BillingSpecifiedPeriod", *ln.Period)
	}
	for _, a := range ln.Allowances {
		ciiAC(x, a, false, base, false)
	}
	for _, c := range ln.Charges {
		ciiAC(x, c, true, base, false)
	}
	x.start("ram:SpecifiedTradeSettlementLineMonetarySummation")
	x.pmoney("ram:LineTotalAmount", ln.Extension)
	x.end("ram:SpecifiedTradeSettlementLineMonetarySummation")
	if ln.Account != nil {
		ciiAccount(x, *ln.Account)
	}
	x.end("ram:SpecifiedLineTradeSettlement")
	x.end("ram:IncludedSupplyChainTradeLineItem")
}

// ciiPriceBasis writes the quantity the price is quoted for. A price is per
// unit unless the item says otherwise (a price per 1000 for instance), and the
// unit code attribute is mandatory on the quantity.
func ciiPriceBasis(x *xw, ln Line) {
	q, u := 1.0, ln.UnitCode
	if ln.Item.Price != nil {
		if ln.Item.Price.BaseQty != 0 {
			q = ln.Item.Price.BaseQty
		}
		if ln.Item.Price.BaseUnit != "" {
			u = ln.Item.Price.BaseUnit
		}
	}
	x.elem("ram:BasisQuantity", Rate(q), attr{"unitCode", u})
}

func ciiProduct(x *xw, ln Line) {
	it := ln.Item
	x.start("ram:SpecifiedTradeProduct")
	x.opt("ram:GlobalID", it.StandardID, attr{"schemeID", it.StdScheme})
	x.opt("ram:SellerAssignedID", it.SellerID)
	x.opt("ram:BuyerAssignedID", it.CustomerID)
	x.mand("ram:Name", it.Name)
	x.opt("ram:Description", it.Description)
	for _, a := range it.Attributes {
		ciiAttr(x, a)
	}
	for _, a := range ln.Attributes {
		ciiAttr(x, a)
	}
	for _, c := range it.ClassIDs {
		x.start("ram:DesignatedProductClassification")
		x.elem("ram:ClassCode", c, attr{"listID", it.ClassScheme})
		x.end("ram:DesignatedProductClassification")
	}
	if it.Origin != "" {
		x.start("ram:OriginTradeCountry")
		x.mand("ram:ID", it.Origin)
		x.end("ram:OriginTradeCountry")
	}
	x.end("ram:SpecifiedTradeProduct")
}

// ciiAttr writes an item attribute (BT-160s). BR-54 asks for both a name and a
// value, so both are always written.
func ciiAttr(x *xw, a Attribute) {
	x.start("ram:ApplicableProductCharacteristic")
	x.mand("ram:Description", a.Name)
	x.mand("ram:Value", a.Value)
	x.end("ram:ApplicableProductCharacteristic")
}

// ciiAgreementRefs gives the order the schema imposes on the reference
// elements of ApplicableHeaderTradeAgreement: sales order (BT-14), buyer
// order (BT-13), quotation (no term of its own) then contract (BT-12).
// References are bucketed and written in this order, so the caller's
// ordering cannot produce a document the schema rejects.
var ciiAgreementRefs = []string{
	"ram:SellerOrderReferencedDocument",
	"ram:BuyerOrderReferencedDocument",
	"ram:QuotationReferencedDocument",
	"ram:ContractReferencedDocument",
}

func ciiAgreement(x *xw, in Invoice) {
	x.start("ram:ApplicableHeaderTradeAgreement")
	x.opt("ram:BuyerReference", in.BuyerReference)
	ciiParty(x, "ram:SellerTradeParty", in.Seller.Party, in.Seller.TradingName)
	ciiParty(x, "ram:BuyerTradeParty", in.Buyer, "")

	// Incoterms and the place of delivery. Neither is an EN 16931 business
	// term, so this is the only slot CII offers for them and the schematron
	// advises against it (CII-SR-293); UBL has no slot and drops both.
	if in.Delivery != nil && (in.Delivery.ShipTrade != "" || in.Delivery.ShipPlace != "") {
		x.start("ram:ApplicableTradeDeliveryTerms")
		x.opt("ram:DeliveryTypeCode", in.Delivery.ShipTrade)
		if in.Delivery.ShipPlace != "" {
			x.start("ram:RelevantTradeLocation")
			x.mand("ram:Name", in.Delivery.ShipPlace)
			x.end("ram:RelevantTradeLocation")
		}
		x.end("ram:ApplicableTradeDeliveryTerms")
	}

	// The order of the reference elements is fixed by the schema, so the
	// references are emitted in that order rather than in the order the
	// caller happened to list them.
	var project *Reference
	// Each order, quotation and contract reference has exactly one CII slot,
	// so a second reference of the same kind is a defect rather than a second
	// element: the schema allows only one of each.
	seenRef := map[string]bool{}
	refs := map[string]Reference{}
	one := func(kind, slot string, r Reference) {
		if seenRef[kind] {
			x.badf("only one %s reference fits CII: %q", kind, r.ID)
			return
		}
		seenRef[kind] = true
		refs[slot] = r
	}
	for _, r := range in.References {
		switch r.Type {
		case "salesOrder":
			one("sales order", "ram:SellerOrderReferencedDocument", r)
		case "purchase", "buyerOrder":
			one("buyer order", "ram:BuyerOrderReferencedDocument", r)
		case "quotation":
			// A quotation is not an EN 16931 term either, so this element
			// is advisory (CII-SR-296). It is still the exact place for
			// one; the alternative would be the additional supporting
			// document slot, which needs a type code nobody asked for.
			one("quotation", "ram:QuotationReferencedDocument", r)
		case "contract":
			one("contract", "ram:ContractReferencedDocument", r)
		case "project":
			rr := r
			project = &rr
		case "preceding", "despatch", "deliveryNote":
			// Rendered by the section that owns the element.
		default:
			x.badf("reference type %q has no CII element", r.Type)
		}
	}
	for _, slot := range ciiAgreementRefs {
		if r, ok := refs[slot]; ok {
			ciiRef(x, slot, r)
		}
	}
	if project != nil {
		x.start("ram:SpecifiedProcuringProject")
		x.mand("ram:ID", project.ID)
		x.mand("ram:Name", project.Name)
		x.end("ram:SpecifiedProcuringProject")
	}
	x.end("ram:ApplicableHeaderTradeAgreement")
}

func ciiDelivery(x *xw, in Invoice) {
	x.start("ram:ApplicableHeaderTradeDelivery")
	if d := in.Delivery; d != nil {
		if d.ReceiverID != "" || d.Name != "" || !emptyAddress(d.Address) {
			x.start("ram:ShipToTradeParty")
			x.opt("ram:ID", d.ReceiverID)
			x.mand("ram:Name", d.Name)
			ciiAddress(x, d.Address, "")
			x.end("ram:ShipToTradeParty")
		}
		// A despatch date is beyond EN 16931, which has the one actual
		// delivery date (BT-72, below). CII has an event for despatch and
		// the schematron advises against using it (CII-SR-319), but it is
		// the only place in CII for a date that is not the delivery date.
		if d.DepartureDate != "" {
			x.start("ram:ActualDespatchSupplyChainEvent")
			x.dt("ram:OccurrenceDateTime", d.DepartureDate)
			x.end("ram:ActualDespatchSupplyChainEvent")
		}
		if d.ArrivalDate != "" || d.Period != nil {
			x.start("ram:ActualDeliverySupplyChainEvent")
			if d.ArrivalDate != "" {
				x.dt("ram:OccurrenceDateTime", d.ArrivalDate)
			}
			if d.Period != nil {
				// A period around the delivery itself is not an EN 16931
				// term - the invoicing period (BG-14) is the one on the
				// invoice - and the schematron advises against this element
				// (CII-SR-329), so it is written only when asked for.
				ciiPeriod(x, "ram:OccurrenceSpecifiedPeriod", *d.Period)
			}
			x.end("ram:ActualDeliverySupplyChainEvent")
		}
	}
	// Despatch and delivery note references. Neither is a business term in
	// EN 16931, and the CII mapping does not use either element
	// (CII-SR-173, CII-SR-335); UBL carries both as ordinary document
	// references. They stay because a caller who has a despatch advice in
	// hand should be able to cite it somewhere.
	for _, r := range in.References {
		switch r.Type {
		case "despatch":
			ciiRef(x, "ram:DespatchAdviceReferencedDocument", r)
		case "deliveryNote":
			ciiRef(x, "ram:DeliveryNoteReferencedDocument", r)
		}
	}
	x.end("ram:ApplicableHeaderTradeDelivery")
}

func ciiSettlement(x *xw, in Invoice) {
	x.start("ram:ApplicableHeaderTradeSettlement")
	if in.Payment != nil {
		x.opt("ram:PaymentReference", in.Payment.RemitInfo)
	}
	x.mand("ram:InvoiceCurrencyCode", in.Currency)
	if in.Payee != nil {
		ciiParty(x, "ram:PayeeTradeParty", *in.Payee, "")
	}
	ciiPaymentMeans(x, in)

	for _, v := range in.VATBreakdown {
		ciiVAT(x, v)
	}
	if in.Period != nil {
		ciiPeriod(x, "ram:BillingSpecifiedPeriod", *in.Period)
	}

	// A document level percentage is taken on the total before VAT, charges
	// first (Derive uses the same base, so the two cannot disagree).
	lineBasis, _ := lineTotalBasis(&in).Float64()
	chargeBasis := lineBasis
	if in.Totals.ChargeTotal != nil {
		chargeBasis += *in.Totals.ChargeTotal
	}
	for _, c := range in.Charges {
		ciiAC(x, c, true, lineBasis, true)
	}
	for _, a := range in.Allowances {
		ciiAC(x, a, false, chargeBasis, true)
	}

	mandate := ""
	if in.Payment != nil && in.Payment.Mandate != nil {
		mandate = in.Payment.Mandate.ID
	}
	if len(in.Terms) > 0 || in.DueDate != "" || mandate != "" {
		x.start("ram:SpecifiedTradePaymentTerms")
		for _, t := range in.Terms {
			x.mand("ram:Description", t)
		}
		if in.DueDate != "" {
			x.dt("ram:DueDateDateTime", in.DueDate)
		}
		x.opt("ram:DirectDebitMandateID", mandate)
		x.end("ram:SpecifiedTradePaymentTerms")
	}

	x.start("ram:SpecifiedTradeSettlementHeaderMonetarySummation")
	x.pmoney("ram:LineTotalAmount", in.Totals.LineTotal)
	x.pmoney("ram:ChargeTotalAmount", in.Totals.ChargeTotal)
	x.pmoney("ram:AllowanceTotalAmount", in.Totals.AllowanceTotal)
	x.pmoney("ram:TaxBasisTotalAmount", in.Totals.TaxExclusive)
	if in.Totals.TaxTotal != nil {
		x.elem("ram:TaxTotalAmount", Money(*in.Totals.TaxTotal), attr{"currencyID", in.Currency})
	} else {
		x.bad("TaxTotalAmount is missing")
	}
	x.omoney("ram:RoundingAmount", in.Rounding)
	x.pmoney("ram:GrandTotalAmount", in.Totals.TaxInclusive)
	x.omoney("ram:TotalPrepaidAmount", in.Prepaid)
	x.pmoney("ram:DuePayableAmount", in.Totals.Payable)
	x.end("ram:SpecifiedTradeSettlementHeaderMonetarySummation")

	for _, r := range in.References {
		if r.Type == "preceding" {
			ciiRef(x, "ram:InvoiceReferencedDocument", r)
		}
	}
	if in.Accounting != nil {
		ciiAccount(x, *in.Accounting)
	}
	x.end("ram:ApplicableHeaderTradeSettlement")
}

// ciiPaymentMeans is skipped entirely when there is nothing to say: an empty
// payment means block would claim a payment method the invoice does not have.
func ciiPaymentMeans(x *xw, in Invoice) {
	var p Payment
	if in.Payment != nil {
		p = *in.Payment
	}
	debtor := in.BuyerAccount
	if p.Mandate != nil && p.Mandate.Bank != nil {
		debtor = p.Mandate.Bank
	}
	if p.Code == "" && p.Name == "" && p.Card == nil && p.Mandate == nil &&
		in.SellerAccount == nil && debtor == nil {
		return
	}

	x.start("ram:SpecifiedTradeSettlementPaymentMeans")
	x.mand("ram:TypeCode", p.Code)
	x.opt("ram:Information", p.Name)
	if p.Card != nil {
		x.start("ram:ApplicableTradeSettlementFinancialCard")
		x.mand("ram:ID", p.Card.Pan)
		x.opt("ram:TypeCode", p.Card.Type)
		x.opt("ram:CardholderName", p.Card.Holder)
		x.end("ram:ApplicableTradeSettlementFinancialCard")
	}
	if debtor != nil {
		x.start("ram:PayerPartyDebtorFinancialAccount")
		ciiAcct(x, *debtor)
		x.end("ram:PayerPartyDebtorFinancialAccount")
	}
	if in.SellerAccount != nil {
		x.start("ram:PayeePartyCreditorFinancialAccount")
		ciiAcct(x, *in.SellerAccount)
		x.end("ram:PayeePartyCreditorFinancialAccount")
	}
	if in.SellerAccount != nil && in.SellerAccount.BIC != "" {
		x.start("ram:PayeeSpecifiedCreditorFinancialInstitution")
		x.mand("ram:BICID", in.SellerAccount.BIC)
		x.end("ram:PayeeSpecifiedCreditorFinancialInstitution")
	}
	x.end("ram:SpecifiedTradeSettlementPaymentMeans")
}

func ciiAcct(x *xw, a FinancialAcct) {
	x.opt("ram:IBANID", a.IBAN)
	x.opt("ram:AccountName", a.Name)
	x.opt("ram:ProprietaryID", a.OtherID)
}

func ciiVAT(x *xw, v VATRate) {
	x.start("ram:ApplicableTradeTax")
	x.money("ram:CalculatedAmount", v.Amount)
	x.elem("ram:TypeCode", "VAT")
	if v.Exempt != nil {
		x.opt("ram:ExemptionReason", v.Exempt.Reason)
	}
	x.money("ram:BasisAmount", v.Taxable)
	x.mand("ram:CategoryCode", v.Category)
	if v.Exempt != nil {
		x.opt("ram:ExemptionReasonCode", v.Exempt.ReasonCode)
	}
	if v.Category != "O" {
		x.elem("ram:RateApplicablePercent", Rate(v.Percent))
	}
	x.end("ram:ApplicableTradeTax")
}

// ciiAC writes one allowance or charge. A percentage is written with its
// percentage and the amount it was taken on, and the resolved amount is written
// either way, because the validator checks the amount (BR-31) and a percentage
// on its own does not say what was deducted. Only a document level allowance or
// charge carries tax information of its own (BT-95, BT-96): a line level one
// inherits the VAT of its line, and CII-SR-191 warns away a tax block there.
func ciiAC(x *xw, a AllowanceCharge, charge bool, basis float64, tax bool) {
	x.start("ram:SpecifiedTradeAllowanceCharge")
	x.indicator(charge)
	if a.Amount == 0 && a.Percent != nil {
		x.elem("ram:CalculationPercent", Rate(*a.Percent))
		x.money("ram:BasisAmount", basis)
	}
	x.money("ram:ActualAmount", acAmount(a, basis))
	x.opt("ram:ReasonCode", a.ReasonCode)
	x.opt("ram:Reason", a.Reason)
	if tax && a.TaxCategory != "" {
		x.start("ram:CategoryTradeTax")
		x.elem("ram:TypeCode", "VAT")
		x.mand("ram:CategoryCode", a.TaxCategory)
		if a.TaxCategory != "O" && a.TaxRate != nil {
			x.elem("ram:RateApplicablePercent", Rate(*a.TaxRate))
		}
		x.end("ram:CategoryTradeTax")
	}
	x.end("ram:SpecifiedTradeAllowanceCharge")
}

// acAmount resolves an allowance or charge to the amount to write: an explicit
// amount, or the percentage of the basis it was taken on.
func acAmount(a AllowanceCharge, basis float64) float64 {
	f, _ := allowanceAmount(a, ratOf(basis)).Float64()
	return f
}

func ciiRef(x *xw, name string, r Reference) {
	x.start(name)
	x.mand("ram:IssuerAssignedID", r.ID)
	x.end(name)
}

// ciiPeriod writes a BillingSpecifiedPeriod. The period narrative is not a
// business term and has no home in CII: the standard forbids Description inside
// BillingSpecifiedPeriod outright (CII-DT-071), so it is dropped here and
// only UBL can carry it.
func ciiPeriod(x *xw, name string, p Period) {
	x.start(name)
	if p.Start != "" {
		x.dt("ram:StartDateTime", p.Start)
	}
	if p.End != "" {
		x.dt("ram:EndDateTime", p.End)
	}
	x.end(name)
}

func ciiAccount(x *xw, a Account) {
	x.start("ram:ReceivableSpecifiedTradeAccountingAccount")
	x.mand("ram:ID", a.ID)
	x.opt("ram:TypeCode", a.Type)
	x.end("ram:ReceivableSpecifiedTradeAccountingAccount")
}

// ciiParty writes a party. The trading name belongs to the legal organisation,
// while the party name stays the legal name (BT-27 for the seller, BT-44 for
// the buyer); the legal registration identifiers (BT-30, BT-47) go into the
// legal organisation's ID, which unlike GlobalID has no scheme to get wrong.
func ciiParty(x *xw, name string, p Party, trading string) {
	x.start(name)
	if p.Identifier != "" {
		x.elem("ram:ID", p.Identifier, attr{"schemeID", p.IDScheme})
	}
	x.mand("ram:Name", p.Name)
	if len(p.RegIDs) > 0 || trading != "" {
		x.start("ram:SpecifiedLegalOrganization")
		if len(p.RegIDs) > 0 {
			x.mand("ram:ID", p.RegIDs[0])
			for _, extra := range p.RegIDs[1:] {
				x.badf("CII has room for one legal registration identifier, not %q", extra)
			}
		}
		x.opt("ram:TradingBusinessName", trading)
		x.end("ram:SpecifiedLegalOrganization")
	}
	if p.Contact != nil {
		x.start("ram:DefinedTradeContact")
		x.opt("ram:PersonName", p.Contact.Name)
		if p.Contact.Phone != "" {
			x.start("ram:TelephoneUniversalCommunication")
			x.mand("ram:CompleteNumber", p.Contact.Phone)
			x.end("ram:TelephoneUniversalCommunication")
		}
		if p.Contact.Email != "" {
			x.start("ram:EmailURIUniversalCommunication")
			x.mand("ram:URIID", p.Contact.Email)
			x.end("ram:EmailURIUniversalCommunication")
		}
		x.end("ram:DefinedTradeContact")
	}
	ciiAddress(x, p.Address, p.Subdiv)
	if p.EAddress != "" {
		x.start("ram:URIUniversalCommunication")
		x.elem("ram:URIID", p.EAddress, attr{"schemeID", p.EASCode()})
		x.end("ram:URIUniversalCommunication")
	}
	for _, t := range p.TaxIDs {
		x.start("ram:SpecifiedTaxRegistration")
		x.elem("ram:ID", t.Value, attr{"schemeID", ciiTaxScheme(t.Scheme)})
		x.end("ram:SpecifiedTaxRegistration")
	}
	x.end(name)
}

func ciiAddress(x *xw, a Address, subdiv string) {
	x.start("ram:PostalTradeAddress")
	x.opt("ram:PostcodeCode", a.Postal)
	x.opt("ram:LineOne", a.Line1)
	x.opt("ram:LineTwo", a.Line2)
	x.opt("ram:LineThree", a.Line3)
	x.opt("ram:CityName", a.City)
	x.mand("ram:CountryID", a.Country)
	zone := a.Zone
	if zone == "" {
		zone = subdiv
	}
	x.opt("ram:CountrySubDivisionName", zone)
	x.end("ram:PostalTradeAddress")
}

// ciiTaxScheme translates the scheme label a caller writes for a tax
// registration number into the one CII expects. The rules key on schemeID
// literally (BR-CO-26 looks for 'VA'), so "VAT" - which is what UBL and most
// people write - has to become "VA"; "FC" is the fiscal number used by e.g.
// the German profiles.
func ciiTaxScheme(s string) string {
	switch s {
	case "", "VAT":
		return "VA"
	default:
		return s
	}
}

func emptyAddress(a Address) bool {
	return a.Line1 == "" && a.Line2 == "" && a.Line3 == "" &&
		a.City == "" && a.Postal == "" && a.Country == ""
}
