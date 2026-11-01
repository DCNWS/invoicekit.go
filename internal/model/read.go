package model

import (
	"bytes"
	"encoding/xml"
	"fmt"
	"io"
	"maps"
	"math"
	"strconv"
	"strings"
)

// Reading a document is the renderer's mirror image: the same schema order, the
// same element-by-element decisions, run backwards. That makes the reader the
// other half of a conversion (a customer's CII invoice becomes the model, the
// model becomes a UBL invoice), so it has to be as deliberate about what each
// element means as the writers are.
//
// Two rules hold throughout.
//
// A document's stated figures are kept, never recomputed: reading is not
// deriving. An amount the document does not carry stays nil, which the model
// already means as "compute me".
//
// Anything the document carries that the model has no term for is reported in
// ReadReport.Issues rather than dropped in silence. The set of such elements is
// exactly the set of places where a conversion loses information, so a new
// entry is a decision to take on a loss.

// ReadReport is the result of reading a document: the invoice as the document
// states it, and the elements it carries that the model cannot hold.
type ReadReport struct {
	Invoice Invoice  `json:"invoice"`
	Issues  []string `json:"issues,omitempty"`
}

// vnode is one parsed XML element.
//
// Prefixes are resolved to namespaces and then written back in the package's
// canonical prefixes, so a document that calls the UBL common aggregate
// components "bac:" or puts the invoice root in the default namespace reads
// just as well as one written by this package.
type vnode struct {
	q      string            // canonical qualified name, e.g. "ram:ID"
	local  string            // local name, with no namespace qualifier
	attr   map[string]string // attribute name to value
	text   string            // character data directly inside the element
	kids   []*vnode
	parent *vnode
	seen   bool // marked by the reader once the element has been mapped
}

// nsPrefix canonicalises the namespaces both syntaxes use.
var nsPrefix = map[string]string{
	NSRSM: "rsm", NSRAM: "ram", NSUDT: "udt",
	NSCAC: "cac", NSCBC: "cbc", NSUBL: "inv", NSCREDIT: "credit",
}

// parseDoc builds the element tree. Namespace declarations are tracked by scope
// so that element names can be resolved to their namespaces.
func parseDoc(data []byte) (*vnode, error) {
	dec := xml.NewDecoder(bytes.NewReader(data))
	doc := &vnode{attr: map[string]string{}}
	stack := []*vnode{doc}
	scopes := []map[string]string{{"": ""}}
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("cannot parse the XML: %w", err)
		}
		switch tk := tok.(type) {
		case xml.StartElement:
			scope := cloneScope(scopes[len(scopes)-1])
			for _, a := range tk.Attr {
				if a.Name.Space == "xmlns" {
					scope[a.Name.Local] = a.Value
				} else if a.Name.Local == "xmlns" {
					scope[""] = a.Value
				}
			}
			// encoding/xml resolves a prefix to its namespace itself, so the
			// name is usually already a URI; the scope covers a document that
			// writes a bare prefix without declaring it.
			uri := tk.Name.Space
			if u, ok := scope[tk.Name.Space]; ok {
				uri = u
			}
			q := tk.Name.Local
			if p := nsPrefix[uri]; p != "" {
				q = p + ":" + q
			} else if uri != "" {
				q = prefixOf(scope, uri) + ":" + q
			}
			n := &vnode{q: q, local: tk.Name.Local, attr: map[string]string{}, parent: stack[len(stack)-1]}
			for _, a := range tk.Attr {
				if a.Name.Space == "xmlns" || a.Name.Local == "xmlns" {
					continue
				}
				n.attr[a.Name.Local] = a.Value
			}
			stack[len(stack)-1].kids = append(stack[len(stack)-1].kids, n)
			stack = append(stack, n)
			scopes = append(scopes, scope)
		case xml.EndElement:
			if len(stack) > 1 {
				stack = stack[:len(stack)-1]
				scopes = scopes[:len(scopes)]
			}
		case xml.CharData:
			cur := stack[len(stack)-1]
			cur.text += string(tk)
		}
	}
	if len(doc.kids) == 0 {
		return nil, fmt.Errorf("cannot parse the XML: no document element")
	}
	return doc, nil
}

// prefixOf names the prefix a scope binds to a namespace, or the namespace
// itself when nothing binds it, so an unfamiliar namespace still reads as a
// qualified name in a message rather than as a bare URL glued to a name.
func prefixOf(scope map[string]string, uri string) string {
	for p, u := range scope {
		if u == uri && p != "" {
			return p
		}
	}
	return uri
}

func cloneScope(s map[string]string) map[string]string {
	out := make(map[string]string, len(s)+1)
	maps.Copy(out, s)
	return out
}

// reader carries the invoice being built, the findings, and the syntax it is
// reading, which decides only how a term is spelled in a message.
type reader struct {
	sx     Syntax
	inv    Invoice
	issues []string
	errs   []string
	// seen holds the terms the syntax gives only one element to, so that a
	// document carrying two of them says so instead of silently keeping one.
	seen map[string]bool
}

// once reports the second use of a term the model has one place for. The reader
// keeps what it read first and says what it dropped, because a document that
// carries two payment means blocks is still a document worth reading.
func (r *reader) once(n *vnode, key, what string) bool {
	if r.seen == nil {
		r.seen = map[string]bool{}
	}
	if r.seen[key] {
		r.issues = append(r.issues, fmt.Sprintf("%s: only one %s fits the model, so the rest of it is dropped", pathOf(n), what))
		return false
	}
	r.seen[key] = true
	return true
}

// apply maps n's children through h, in document order. A child with no handler
// is left unmarked so that unmapped can report it.
func (r *reader) apply(n *vnode, h map[string]func(*vnode)) {
	n.seen = true
	for _, c := range n.kids {
		if f, ok := h[c.q]; ok {
			c.seen = true
			f(c)
		}
	}
}

func (r *reader) find(n *vnode, q string) *vnode {
	if n == nil {
		return nil
	}
	for _, c := range n.kids {
		if c.q == q {
			return c
		}
	}
	return nil
}

// consume marks an element and everything under it as mapped, which is how a
// wrapper that carries nothing the model has a term for is accounted for:
// cac:TaxScheme, a CII date wrapper's udt:DateTimeString, a charge indicator.
func (r *reader) consume(n *vnode) {
	if n == nil {
		return
	}
	n.seen = true
	for _, c := range n.kids {
		r.consume(c)
	}
}

// unmapped reports every element the reader never reached, naming the topmost
// one and skipping its subtree, since the reason a nested element is unmapped
// is the element above it.
func (r *reader) unmapped(n *vnode) {
	for _, c := range n.kids {
		if c.seen {
			continue
		}
		r.issues = append(r.issues, fmt.Sprintf("%s: %s has no place in the EN 16931 model, so it is dropped", pathOf(c), c.q))
		r.consume(c)
	}
	for _, c := range n.kids {
		r.unmapped(c)
	}
}

func (r *reader) badf(n *vnode, format string, args ...any) {
	r.errs = append(r.errs, pathOf(n)+": "+fmt.Sprintf(format, args...))
}

func (r *reader) errf(prefix string) error {
	return fmt.Errorf("%s: cannot read: %s", prefix, strings.Join(r.errs, "; "))
}

// pathOf names the element an error was found in, by its ancestry.
func pathOf(n *vnode) string {
	var parts []string
	for c := n; c != nil && c.q != ""; c = c.parent {
		parts = append(parts, c.q)
	}
	for i, j := 0, len(parts)-1; i < j; i, j = i+1, j-1 {
		parts[i], parts[j] = parts[j], parts[i]
	}
	return strings.Join(parts, "/")
}

// The leaf readers. Each marks the element it reads, so an element that has
// been read cannot be reported as unmapped.

func text(n *vnode) string {
	if n == nil {
		return ""
	}
	return strings.TrimSpace(n.text)
}

func (r *reader) str(n *vnode, q string) string { return text(r.takeChild(n, q)) }

func (r *reader) takeChild(n *vnode, q string) *vnode {
	c := r.find(n, q)
	if c == nil {
		return nil
	}
	c.seen = true
	return c
}

func (r *reader) into(n *vnode, q string, dst *string) {
	if c := r.find(n, q); c != nil {
		c.seen = true
		*dst = text(c)
	}
}

func (r *reader) num(n *vnode) float64 {
	s := text(n)
	v, err := strconv.ParseFloat(s, 64)
	if err != nil {
		r.badf(n, "%q is not a number", s)
		return 0
	}
	return v
}

// numOf reads an optional amount: an element that is not there is a nil, which
// the model reads as "not stated", rather than a zero the document never said.
func (r *reader) numOf(n *vnode, q string) *float64 {
	c := r.find(n, q)
	if c == nil {
		return nil
	}
	c.seen = true
	v := r.num(c)
	return &v
}

func (r *reader) qty(n *vnode) (float64, string) {
	return r.num(n), n.attr["unitCode"]
}

// dateOf reads a date from the element that holds it: plain ISO text in UBL, a
// udt:DateTimeString with format 102 in CII.
func (r *reader) dateOf(n *vnode) string {
	if n == nil {
		return ""
	}
	if inner := r.find(n, "udt:DateTimeString"); inner != nil {
		r.consume(inner)
		return isoDate(text(inner))
	}
	return isoDate(text(n))
}

func (r *reader) date(n *vnode, q string) string { return r.dateOf(r.takeChild(n, q)) }

// ind reads a boolean indicator, whichever shape the syntax gives it.
func (r *reader) ind(n *vnode, q string) bool {
	c := r.find(n, q)
	if c == nil {
		return false
	}
	s := text(c)
	if inner := r.find(c, "udt:Indicator"); inner != nil {
		s = text(inner)
	}
	r.consume(c)
	return s == "true" || s == "1"
}

// exempt reads the two halves of an exemption, and reports nothing when the
// document states neither.
func (r *reader) exempt(n *vnode, reasonq, codeq string) *TaxExempt {
	reason, code := r.str(n, reasonq), r.str(n, codeq)
	if reason == "" && code == "" {
		return nil
	}
	return &TaxExempt{Reason: reason, ReasonCode: code}
}

// period reads a period. The element's presence is what decides whether the
// model has a period at all, so an empty one stays an empty one.
func (r *reader) period(n *vnode) *Period {
	p := Period{Start: r.date(n, "ram:StartDateTime"), End: r.date(n, "ram:EndDateTime")}
	if s, e := r.find(n, "cbc:StartDate"), r.find(n, "cbc:EndDate"); s != nil || e != nil {
		p.Start, p.End = r.date(n, "cbc:StartDate"), r.date(n, "cbc:EndDate")
	}
	return &p
}

// account reads a bank account. UBL gives one identifier where CII separates an
// IBAN from a private account number, so a plausible IBAN is sorted into the
// IBAN field and anything else into the other identifier.
func (r *reader) account(n *vnode, idName, bicWrap string) FinancialAcct {
	a := FinancialAcct{}
	r.into(n, "ram:IBANID", &a.IBAN)
	r.into(n, "ram:AccountName", &a.Name)
	r.into(n, "ram:ProprietaryID", &a.OtherID)
	if id := r.takeChild(n, idName); id != nil {
		v := text(id)
		if isIBAN(v) {
			a.IBAN = v
		} else {
			a.OtherID = v
		}
	}
	r.into(n, "cbc:Name", &a.Name)
	if b := r.find(n, "cac:FinancialInstitutionBranch"); b != nil {
		r.take(b)
		r.into(b, "cbc:ID", &a.BIC)
	}
	if b := r.find(n, bicWrap); b != nil {
		r.take(b)
		r.into(b, "ram:BICID", &a.BIC)
	}
	return a
}

// isIBAN reports whether an account identifier has the shape of an IBAN: a two
// letter country code, two check digits and at least ten more characters.
// Enough to sort a BBAN from an IBAN in practice, and deliberately not a
// checksum test, since a mistyped IBAN is the sender's problem, not the
// reader's.
func isIBAN(s string) bool {
	if len(s) < 15 || len(s) > 34 {
		return false
	}
	up := func(b byte) bool { return b >= 'A' && b <= 'Z' }
	if !up(s[0]) || !up(s[1]) {
		return false
	}
	if s[2] < '0' || s[2] > '9' || s[3] < '0' || s[3] > '9' {
		return false
	}
	for i := 4; i < len(s); i++ {
		c := s[i]
		if !(c >= 'A' && c <= 'Z' || c >= '0' && c <= '9') {
			return false
		}
	}
	return true
}

// priceRead holds what a document says about a line's price before the model is
// assembled. The base quantity is read into it because the line's unit code,
// which decides whether the base says anything at all, sits in a different part
// of the schema from the price.
type priceRead struct {
	q     *float64
	u     string
	gross *float64
	disc  *float64
}

// finishPrice assembles the price from what the document stated, measured the
// way the writers measure it: a base quantity is only worth recording when it
// differs from one unit of the line's own unit, and a discount only when there
// is one.
func finishPrice(ln *Line, pr priceRead) {
	q, u := 1.0, ln.UnitCode
	if pr.q != nil && *pr.q != 0 {
		q = *pr.q
	}
	if pr.u != "" {
		u = pr.u
	}
	var p *ItemPrice
	if q != 1 || u != ln.UnitCode {
		p = &ItemPrice{BaseQty: q, BaseUnit: u}
	}
	if pr.disc != nil && *pr.disc != 0 {
		if p == nil {
			p = &ItemPrice{}
		}
		p.Allowance = pr.disc
	}
	ln.Item.Price = p
	if pr.gross != nil {
		ln.GrossPrice = *pr.gross
	}
}

// readAC fills the halves an allowance or charge shares between the syntaxes.
// The stated amount is recorded only when the document states no percentage:
// the model treats an empty amount as "derive it from the percentage", so
// writing the document's own figure back would put the model at odds with its
// own arithmetic. The mismatch is reported instead.
func (r *reader) readAC(n *vnode, ac *AllowanceCharge, percentName, amountName, basisName string) {
	if b := r.numOf(n, basisName); b != nil {
		amt := r.numOf(n, amountName)
		ac.Percent = r.numOf(n, percentName)
		if ac.Percent != nil && amt != nil {
			r.checkAC(n, *ac.Percent, *b, *amt)
		} else if amt != nil {
			ac.Amount = *amt
		}
		return
	}
	if amt := r.numOf(n, amountName); amt != nil {
		ac.Amount = *amt
	}
	if p := r.numOf(n, percentName); p != nil {
		ac.Percent = p
	}
}

// checkAC reports a document whose stated amount is not the percentage it
// gives of the basis it gives.
func (r *reader) checkAC(n *vnode, pct, basis, stated float64) {
	want, _ := allowanceAmount(AllowanceCharge{Percent: &pct}, ratOf(basis)).Float64()
	if math.Abs(want-stated) <= 0.005 {
		return
	}
	r.issues = append(r.issues, fmt.Sprintf("%s: %s%% of %s is %s, not the %s the document states",
		pathOf(n), Rate(pct), Money(basis), Money(want), Money(stated)))
}

// take marks an element as mapped without marking what is under it. Use it for
// a wrapper whose children are read one by one, so that an element inside the
// wrapper which the reader has no handler for is still reported.
func (r *reader) take(n *vnode) {
	if n != nil {
		n.seen = true
	}
}

// children returns every child of n with the name q, marking each, which is how
// a repeating element (notes, tax breakdown lines, allowances) is read.
func (r *reader) children(n *vnode, q string) []*vnode {
	if n == nil {
		return nil
	}
	var out []*vnode
	for _, c := range n.kids {
		if c.q == q {
			c.seen = true
			out = append(out, c)
		}
	}
	return out
}

// lazy groups exist so that a document saying nothing about them leaves the
// model silent too: an invoice with no delivery block must not come back with
// an empty one, because the reader would then be inventing a business group.
func (r *reader) payment() *Payment {
	if r.inv.Payment == nil {
		r.inv.Payment = &Payment{}
	}
	return r.inv.Payment
}

func (r *reader) delivery() *Delivery {
	if r.inv.Delivery == nil {
		r.inv.Delivery = &Delivery{}
	}
	return r.inv.Delivery
}

func amount(p *float64) float64 {
	if p == nil {
		return 0
	}
	return *p
}

// ReadCII reads a CII (UN/CEFACT Cross Industry Invoice) document into the
// semantic model. It returns an error only when the document cannot be read at
// all: the wrong root element, XML that does not parse, or an amount that is not
// an amount. Elements the model has no term for are not errors; they are listed
// in ReadReport.Issues, because a caller converting a document needs to know
// what the conversion lost.
func ReadCII(data []byte) (*ReadReport, error) {
	r := &reader{sx: SyntaxCII}
	doc, err := parseDoc(data)
	if err != nil {
		return nil, err
	}
	root := doc.kids[0]
	if root.q != "rsm:CrossIndustryInvoice" {
		return nil, fmt.Errorf("CII: cannot read: the document element is %s, not rsm:CrossIndustryInvoice", root.q)
	}
	r.apply(root, map[string]func(*vnode){
		"rsm:ExchangedDocumentContext":    r.ciiContext,
		"rsm:ExchangedDocument":           r.ciiDocument,
		"rsm:SupplyChainTradeTransaction": r.ciiTransaction,
	})
	r.unmapped(root)
	if len(r.errs) > 0 {
		return nil, r.errf("CII")
	}
	return &ReadReport{Invoice: r.inv, Issues: r.issues}, nil
}

func (r *reader) ciiContext(n *vnode) {
	r.apply(n, map[string]func(*vnode){
		"ram:BusinessProcessSpecifiedDocumentContextParameter": func(c *vnode) {
			r.inv.ProcessType = r.str(c, "ram:ID")
		},
		"ram:GuidelineSpecifiedDocumentContextParameter": func(c *vnode) {
			r.inv.Guideline = r.str(c, "ram:ID")
		},
	})
}

func (r *reader) ciiDocument(n *vnode) {
	r.apply(n, map[string]func(*vnode){
		"ram:ID":            func(c *vnode) { r.inv.ID = text(c) },
		"ram:TypeCode":      func(c *vnode) { r.inv.TypeCode = text(c) },
		"ram:IssueDateTime": func(c *vnode) { r.inv.IssueDate = r.dateOf(c) },
		"ram:IncludedNote": func(c *vnode) {
			r.inv.Notes = append(r.inv.Notes, r.str(c, "ram:Content"))
		},
	})
}

func (r *reader) ciiTransaction(n *vnode) {
	r.apply(n, map[string]func(*vnode){
		"ram:IncludedSupplyChainTradeLineItem": r.ciiLine,
		"ram:ApplicableHeaderTradeAgreement":   r.ciiAgreement,
		"ram:ApplicableHeaderTradeDelivery":    r.ciiDelivery,
		"ram:ApplicableHeaderTradeSettlement":  r.ciiSettlement,
	})
}

// ciiRef reads one of CII's document references, each of which has exactly one
// slot, so the element name decides the reference's type.
func (r *reader) ciiRef(n *vnode, kind string) {
	r.inv.References = append(r.inv.References, Reference{Type: kind, ID: r.str(n, "ram:IssuerAssignedID")})
}

func (r *reader) ciiAgreement(n *vnode) {
	r.apply(n, map[string]func(*vnode){
		"ram:BuyerReference": func(c *vnode) { r.inv.BuyerReference = text(c) },
		"ram:SellerTradeParty": func(c *vnode) {
			r.inv.Seller.Party, r.inv.Seller.TradingName = r.ciiParty(c)
		},
		"ram:BuyerTradeParty": func(c *vnode) {
			p, _ := r.ciiParty(c)
			r.inv.Buyer = p
		},
		"ram:ApplicableTradeDeliveryTerms": func(c *vnode) {
			d := r.delivery()
			d.ShipTrade = r.str(c, "ram:DeliveryTypeCode")
			if loc := r.find(c, "ram:RelevantTradeLocation"); loc != nil {
				r.take(loc)
				d.ShipPlace = r.str(loc, "ram:Name")
			}
		},
		"ram:SellerOrderReferencedDocument": func(c *vnode) { r.ciiRef(c, "salesOrder") },
		"ram:BuyerOrderReferencedDocument":  func(c *vnode) { r.ciiRef(c, "buyerOrder") },
		"ram:QuotationReferencedDocument":   func(c *vnode) { r.ciiRef(c, "quotation") },
		"ram:ContractReferencedDocument":    func(c *vnode) { r.ciiRef(c, "contract") },
		"ram:SpecifiedProcuringProject": func(c *vnode) {
			r.inv.References = append(r.inv.References, Reference{
				Type: "project",
				ID:   r.str(c, "ram:ID"),
				Name: r.str(c, "ram:Name"),
			})
		},
	})
}

func (r *reader) ciiDelivery(n *vnode) {
	r.apply(n, map[string]func(*vnode){
		"ram:ShipToTradeParty": func(c *vnode) {
			d := r.delivery()
			d.ReceiverID = r.str(c, "ram:ID")
			d.Name = r.str(c, "ram:Name")
			if a := r.find(c, "ram:PostalTradeAddress"); a != nil {
				r.take(a)
				d.Address = r.ciiAddress(a)
			}
		},
		"ram:ActualDespatchSupplyChainEvent": func(c *vnode) {
			r.delivery().DepartureDate = r.date(c, "ram:OccurrenceDateTime")
		},
		"ram:ActualDeliverySupplyChainEvent": func(c *vnode) {
			d := r.delivery()
			d.ArrivalDate = r.date(c, "ram:OccurrenceDateTime")
			if p := r.find(c, "ram:OccurrenceSpecifiedPeriod"); p != nil {
				p.seen = true
				d.Period = r.period(p)
			}
		},
		"ram:DespatchAdviceReferencedDocument": func(c *vnode) { r.ciiRef(c, "despatch") },
		"ram:DeliveryNoteReferencedDocument":   func(c *vnode) { r.ciiRef(c, "deliveryNote") },
	})
}

func (r *reader) ciiSettlement(n *vnode) {
	// The mandate reference sits after the payment means in the schema, but it
	// decides which account the payment means' debtor account belongs to, so the
	// payment terms are read first.
	if t := r.find(n, "ram:SpecifiedTradePaymentTerms"); t != nil {
		t.seen = true
		r.ciiTerms(t)
	}
	r.apply(n, map[string]func(*vnode){
		"ram:PaymentReference":    func(c *vnode) { r.payment().RemitInfo = text(c) },
		"ram:InvoiceCurrencyCode": func(c *vnode) { r.inv.Currency = text(c) },
		"ram:PayeeTradeParty": func(c *vnode) {
			p, _ := r.ciiParty(c)
			r.inv.Payee = &p
		},
		"ram:SpecifiedTradeSettlementPaymentMeans":            r.ciiPaymentMeans,
		"ram:ApplicableTradeTax":                              r.ciiVATLine,
		"ram:BillingSpecifiedPeriod":                          func(c *vnode) { r.inv.Period = r.period(c) },
		"ram:SpecifiedTradeAllowanceCharge":                   r.ciiAC,
		"ram:SpecifiedTradeSettlementHeaderMonetarySummation": r.ciiTotals,
		"ram:InvoiceReferencedDocument":                       func(c *vnode) { r.ciiRef(c, "preceding") },
		"ram:ReceivableSpecifiedTradeAccountingAccount": func(c *vnode) {
			r.inv.Accounting = &Account{ID: r.str(c, "ram:ID"), Type: r.str(c, "ram:TypeCode")}
		},
	})
}

func (r *reader) ciiTerms(n *vnode) {
	for _, d := range r.children(n, "ram:Description") {
		if s := text(d); s != "" {
			r.inv.Terms = append(r.inv.Terms, s)
		}
	}
	r.inv.DueDate = r.date(n, "ram:DueDateDateTime")
	if id := r.str(n, "ram:DirectDebitMandateID"); id != "" {
		r.payment().Mandate = &Mandate{ID: id}
	}
}

// assignNum copies a stated amount into its model field, and leaves the field
// alone when the document states no amount at all.
func assignNum(src *float64, dst **float64) {
	if src != nil {
		*dst = src
	}
}

func (r *reader) ciiTotals(n *vnode) {
	t := &r.inv.Totals
	set := assignNum
	set(r.numOf(n, "ram:LineTotalAmount"), &t.LineTotal)
	set(r.numOf(n, "ram:ChargeTotalAmount"), &t.ChargeTotal)
	set(r.numOf(n, "ram:AllowanceTotalAmount"), &t.AllowanceTotal)
	set(r.numOf(n, "ram:TaxBasisTotalAmount"), &t.TaxExclusive)
	set(r.numOf(n, "ram:TaxTotalAmount"), &t.TaxTotal)
	set(r.numOf(n, "ram:RoundingAmount"), &r.inv.Rounding)
	set(r.numOf(n, "ram:GrandTotalAmount"), &t.TaxInclusive)
	set(r.numOf(n, "ram:TotalPrepaidAmount"), &r.inv.Prepaid)
	set(r.numOf(n, "ram:DuePayableAmount"), &t.Payable)
}

func (r *reader) ciiPaymentMeans(n *vnode) {
	p := r.payment()
	p.Code = r.str(n, "ram:TypeCode")
	p.Name = r.str(n, "ram:Information")
	if c := r.find(n, "ram:ApplicableTradeSettlementFinancialCard"); c != nil {
		r.take(c)
		p.Card = &Card{
			Pan:    r.str(c, "ram:ID"),
			Type:   r.str(c, "ram:TypeCode"),
			Holder: r.str(c, "ram:CardholderName"),
		}
	}
	// A debtor account is the account a mandate debits when there is a mandate,
	// and the buyer's own account otherwise.
	if d := r.find(n, "ram:PayerPartyDebtorFinancialAccount"); d != nil {
		r.take(d)
		a := r.account(d, "", "")
		if p.Mandate != nil {
			p.Mandate.Bank = &a
		} else {
			r.inv.BuyerAccount = &a
		}
	}
	if a := r.find(n, "ram:PayeePartyCreditorFinancialAccount"); a != nil {
		r.take(a)
		acct := r.account(a, "", "")
		r.inv.SellerAccount = &acct
	}
	if b := r.find(n, "ram:PayeeSpecifiedCreditorFinancialInstitution"); b != nil {
		r.take(b)
		if bic := r.str(b, "ram:BICID"); bic != "" {
			if r.inv.SellerAccount == nil {
				r.inv.SellerAccount = &FinancialAcct{}
			}
			r.inv.SellerAccount.BIC = bic
		}
	}
}

// ciiVATLine reads one line of the document level VAT breakdown. Line level tax
// sits elsewhere in the document, so only the settlement's own ApplicableTradeTax
// elements reach here.
func (r *reader) ciiVATLine(n *vnode) {
	v := VATRate{}
	if a := r.numOf(n, "ram:CalculatedAmount"); a != nil {
		v.Amount = *a
	}
	if a := r.numOf(n, "ram:BasisAmount"); a != nil {
		v.Taxable = *a
	}
	v.Category = r.str(n, "ram:CategoryCode")
	if p := r.numOf(n, "ram:RateApplicablePercent"); p != nil {
		v.Percent = *p
	}
	v.Exempt = r.exempt(n, "ram:ExemptionReason", "ram:ExemptionReasonCode")
	if kind := r.str(n, "ram:TypeCode"); kind != "" && kind != "VAT" {
		r.issues = append(r.issues, fmt.Sprintf("%s: a tax type of %s has no place in the EN 16931 model, which knows only VAT", pathOf(n), kind))
	}
	r.inv.VATBreakdown = append(r.inv.VATBreakdown, v)
}

// ciiAC reads one document level allowance or charge.
func (r *reader) ciiAC(n *vnode) {
	a := AllowanceCharge{}
	charge := r.ind(n, "ram:ChargeIndicator")
	r.readAC(n, &a, "ram:CalculationPercent", "ram:ActualAmount", "ram:BasisAmount")
	a.ReasonCode = r.str(n, "ram:ReasonCode")
	a.Reason = r.str(n, "ram:Reason")
	if t := r.find(n, "ram:CategoryTradeTax"); t != nil {
		r.take(t)
		a.TaxCategory = r.str(t, "ram:CategoryCode")
		a.TaxRate = r.numOf(t, "ram:RateApplicablePercent")
		r.str(t, "ram:TypeCode")
	}
	if charge {
		r.inv.Charges = append(r.inv.Charges, a)
		return
	}
	r.inv.Allowances = append(r.inv.Allowances, a)
}

// ciiLine reads one line: its identifier and note, the product, the prices, the
// quantity and the settlement.
func (r *reader) ciiLine(n *vnode) {
	ln := Line{}
	var pr priceRead
	r.apply(n, map[string]func(*vnode){
		"ram:AssociatedDocumentLineDocument": func(c *vnode) {
			ln.ID = r.str(c, "ram:LineID")
			var notes []string
			for _, note := range r.children(c, "ram:IncludedNote") {
				if s := r.str(note, "ram:Content"); s != "" {
					notes = append(notes, s)
				}
			}
			ln.Note = joinLines(notes)
		},
		"ram:SpecifiedTradeProduct":       func(c *vnode) { r.ciiProduct(c, &ln) },
		"ram:SpecifiedLineTradeAgreement": func(c *vnode) { pr = r.ciiLineAgreement(c, &ln) },
		"ram:SpecifiedLineTradeDelivery": func(c *vnode) {
			if q := r.takeChild(c, "ram:BilledQuantity"); q != nil {
				ln.Quantity, ln.UnitCode = r.qty(q)
			}
		},
		"ram:SpecifiedLineTradeSettlement": func(c *vnode) { r.ciiLineSettlement(c, &ln) },
	})
	// The price is assembled last: whether a base quantity says anything
	// depends on the line's unit code, which sits in the delivery block.
	finishPrice(&ln, pr)
	r.inv.Lines = append(r.inv.Lines, ln)
}

func (r *reader) ciiProduct(n *vnode, ln *Line) {
	it := &ln.Item
	if g := r.takeChild(n, "ram:GlobalID"); g != nil {
		it.StandardID = text(g)
		it.StdScheme = g.attr["schemeID"]
	}
	r.into(n, "ram:SellerAssignedID", &it.SellerID)
	r.into(n, "ram:BuyerAssignedID", &it.CustomerID)
	r.into(n, "ram:Name", &it.Name)
	r.into(n, "ram:Description", &it.Description)
	for _, a := range r.children(n, "ram:ApplicableProductCharacteristic") {
		it.Attributes = append(it.Attributes, Attribute{
			Name:  r.str(a, "ram:Description"),
			Value: r.str(a, "ram:Value"),
		})
	}
	for _, c := range r.children(n, "ram:DesignatedProductClassification") {
		code := r.takeChild(c, "ram:ClassCode")
		if code == nil {
			continue
		}
		it.ClassIDs = append(it.ClassIDs, text(code))
		if s := code.attr["listID"]; s != "" {
			it.ClassScheme = s
		}
	}
	if o := r.find(n, "ram:OriginTradeCountry"); o != nil {
		r.take(o)
		it.Origin = r.str(o, "ram:ID")
	}
}

// ciiLineAgreement reads the two price elements. CII hangs the price discount
// (BT-147) off the gross price, and writes the gross block only when it says
// something the net does not, so the absence of the block is what a line with
// one price looks like.
func (r *reader) ciiLineAgreement(n *vnode, ln *Line) priceRead {
	pr := priceRead{}
	if g := r.find(n, "ram:GrossPriceProductTradePrice"); g != nil {
		r.take(g)
		pr.gross = r.numOf(g, "ram:ChargeAmount")
		pr.q, pr.u = r.basisQuantity(g, "ram:BasisQuantity")
		for _, ac := range r.children(g, "ram:AppliedTradeAllowanceCharge") {
			r.take(ac)
			r.ind(ac, "ram:ChargeIndicator")
			pr.disc = r.numOf(ac, "ram:ActualAmount")
		}
	}
	if net := r.find(n, "ram:NetPriceProductTradePrice"); net != nil {
		r.take(net)
		ln.NetPrice = amount(r.numOf(net, "ram:ChargeAmount"))
		// The base quantity belongs to the net price; a document that spells
		// out a gross price repeats it there, and the repeat is what gets
		// read when there is no net one at all.
		if q, u := r.basisQuantity(net, "ram:BasisQuantity"); q != nil {
			pr.q, pr.u = q, u
		}
	}
	return pr
}

// basisQuantity reads the quantity a price is quoted for (BT-149), whichever
// name the syntax gives it.
func (r *reader) basisQuantity(n *vnode, qname string) (*float64, string) {
	q := r.find(n, qname)
	if q == nil {
		return nil, ""
	}
	q.seen = true
	v := r.num(q)
	return &v, q.attr["unitCode"]
}

func (r *reader) ciiLineSettlement(n *vnode, ln *Line) {
	r.apply(n, map[string]func(*vnode){
		"ram:ApplicableTradeTax": func(c *vnode) {
			r.take(c)
			ln.TaxCategory = r.str(c, "ram:CategoryCode")
			if p := r.numOf(c, "ram:RateApplicablePercent"); p != nil {
				ln.TaxRate = *p
			}
			ln.TaxExempt = r.exempt(c, "ram:ExemptionReason", "ram:ExemptionReasonCode")
			r.taxType(c)
		},
		"ram:BillingSpecifiedPeriod": func(c *vnode) { ln.Period = r.period(c) },
		"ram:SpecifiedTradeAllowanceCharge": func(c *vnode) {
			ac, charge := r.ciiACParts(c)
			// CII-SR-191 warns away a tax block on a line correction: it takes
			// the VAT of its line. A document that carries one is reported.
			if t := r.find(c, "ram:CategoryTradeTax"); t != nil {
				r.consume(t)
				r.issues = append(r.issues, fmt.Sprintf("%s: a line allowance or charge takes the VAT of its line, so the tax block there is dropped", pathOf(t)))
			}
			if charge {
				ln.Charges = append(ln.Charges, ac)
				return
			}
			ln.Allowances = append(ln.Allowances, ac)
		},
		"ram:SpecifiedTradeSettlementLineMonetarySummation": func(c *vnode) {
			ln.Extension = r.numOf(c, "ram:LineTotalAmount")
		},
		"ram:ReceivableSpecifiedTradeAccountingAccount": func(c *vnode) {
			ln.Account = &Account{ID: r.str(c, "ram:ID"), Type: r.str(c, "ram:TypeCode")}
		},
	})
}

// taxType accounts for the tax type code, which the model has no room for: EN
// 16931 describes VAT and nothing else.
func (r *reader) taxType(n *vnode) {
	if kind := r.str(n, "ram:TypeCode"); kind != "" && kind != "VAT" {
		r.issues = append(r.issues, fmt.Sprintf("%s: a tax type of %s has no place in the EN 16931 model, which knows only VAT", pathOf(n), kind))
	}
}

// ciiACParts reads the halves an allowance or charge element shares, and says
// which of the two it was.
func (r *reader) ciiACParts(n *vnode) (ac AllowanceCharge, charge bool) {
	charge = r.ind(n, "ram:ChargeIndicator")
	r.readAC(n, &ac, "ram:CalculationPercent", "ram:ActualAmount", "ram:BasisAmount")
	ac.ReasonCode = r.str(n, "ram:ReasonCode")
	ac.Reason = r.str(n, "ram:Reason")
	return ac, charge
}

// ciiParty reads a party and the trading name that sits in its legal
// organisation element, which the model keeps beside the seller rather than in
// the party.
func (r *reader) ciiParty(n *vnode) (Party, string) {
	p := Party{}
	trading := ""
	if id := r.takeChild(n, "ram:ID"); id != nil {
		p.Identifier = text(id)
		p.IDScheme = id.attr["schemeID"]
	}
	r.into(n, "ram:Name", &p.Name)
	if lo := r.find(n, "ram:SpecifiedLegalOrganization"); lo != nil {
		r.take(lo)
		if reg := r.str(lo, "ram:ID"); reg != "" {
			p.RegIDs = []string{reg}
		}
		trading = r.str(lo, "ram:TradingBusinessName")
	}
	if c := r.find(n, "ram:DefinedTradeContact"); c != nil {
		r.take(c)
		ct := Contact{Name: r.str(c, "ram:PersonName")}
		if t := r.find(c, "ram:TelephoneUniversalCommunication"); t != nil {
			r.take(t)
			ct.Phone = r.str(t, "ram:CompleteNumber")
		}
		if e := r.find(c, "ram:EmailURIUniversalCommunication"); e != nil {
			r.take(e)
			ct.Email = r.str(e, "ram:URIID")
		}
		if ct.Name != "" || ct.Phone != "" || ct.Email != "" {
			p.Contact = &ct
		}
	}
	if a := r.find(n, "ram:PostalTradeAddress"); a != nil {
		r.take(a)
		p.Address = r.ciiAddress(a)
	}
	if u := r.find(n, "ram:URIUniversalCommunication"); u != nil {
		r.take(u)
		if id := r.takeChild(u, "ram:URIID"); id != nil {
			p.EAddress = text(id)
			p.EAScheme = id.attr["schemeID"]
		}
	}
	for _, t := range r.children(n, "ram:SpecifiedTaxRegistration") {
		id := r.takeChild(t, "ram:ID")
		if id == nil {
			continue
		}
		scheme := id.attr["schemeID"]
		if scheme == "VA" {
			// CII writes the VAT scheme as VA; the model and UBL call it VAT.
			scheme = "VAT"
		}
		p.TaxIDs = append(p.TaxIDs, TaxID{Value: text(id), Scheme: scheme})
	}
	return p, trading
}

func (r *reader) ciiAddress(n *vnode) Address {
	a := Address{}
	r.into(n, "ram:PostcodeCode", &a.Postal)
	r.into(n, "ram:LineOne", &a.Line1)
	r.into(n, "ram:LineTwo", &a.Line2)
	r.into(n, "ram:LineThree", &a.Line3)
	r.into(n, "ram:CityName", &a.City)
	r.into(n, "ram:CountryID", &a.Country)
	r.into(n, "ram:CountrySubDivisionName", &a.Zone)
	return a
}

// ReadUBL reads an OASIS UBL 2.1 Invoice or CreditNote document into the
// semantic model. UBL and CII describe the same invoice, so both readers fill
// the same model; what differs is the spelling of every element, and the
// occasional term one syntax has no element for.
//
// A credit note reads through the same handlers as an invoice. The two
// documents differ in the name of their root, their type code and their
// quantity element, and in the few elements a credit note has no room for,
// which a credit note document cannot carry in the first place.
func ReadUBL(data []byte) (*ReadReport, error) {
	r := &reader{sx: SyntaxUBL}
	doc, err := parseDoc(data)
	if err != nil {
		return nil, err
	}
	root := doc.kids[0]
	if root.q != "inv:Invoice" && root.q != "credit:CreditNote" {
		return nil, fmt.Errorf("UBL: cannot read: the document element is %s, not inv:Invoice", root.q)
	}
	r.apply(root, map[string]func(*vnode){
		"cbc:CustomizationID":      func(c *vnode) { r.inv.Guideline = text(c) },
		"cbc:ProfileID":            func(c *vnode) { r.inv.ProcessType = text(c) },
		"cbc:ID":                   func(c *vnode) { r.inv.ID = text(c) },
		"cbc:IssueDate":            func(c *vnode) { r.inv.IssueDate = r.dateOf(c) },
		"cbc:DueDate":              func(c *vnode) { r.inv.DueDate = r.dateOf(c) },
		"cbc:InvoiceTypeCode":      func(c *vnode) { r.inv.TypeCode = text(c) },
		"cbc:CreditNoteTypeCode":   func(c *vnode) { r.inv.TypeCode = text(c) },
		"cbc:Note":                 func(c *vnode) { r.inv.Notes = append(r.inv.Notes, text(c)) },
		"cbc:DocumentCurrencyCode": func(c *vnode) { r.inv.Currency = text(c) },
		"cbc:BuyerReference":       func(c *vnode) { r.inv.BuyerReference = text(c) },
		// UBL writes the buyer accounting reference as free text, since its
		// coded counterpart is warned away (UBL-CR-010), so no type comes back.
		"cbc:AccountingCost": func(c *vnode) { r.inv.Accounting = &Account{ID: text(c)} },
		"cac:InvoicePeriod":  func(c *vnode) { r.inv.Period = r.period(c) },
		"cac:OrderReference": r.ublOrder,
		"cac:BillingReference": func(c *vnode) {
			// A document this one looks back on may itself have been a
			// credit note; the model has one slot for either.
			r.apply(c, map[string]func(*vnode){
				"cac:InvoiceDocumentReference":    func(d *vnode) { r.ublRef(d, "preceding") },
				"cac:CreditNoteDocumentReference": func(d *vnode) { r.ublRef(d, "preceding") },
			})
		},
		"cac:DespatchDocumentReference":   func(c *vnode) { r.ublRef(c, "despatch") },
		"cac:ReceiptDocumentReference":    func(c *vnode) { r.ublRef(c, "deliveryNote") },
		"cac:ContractDocumentReference":   func(c *vnode) { r.ublRef(c, "contract") },
		"cac:AdditionalDocumentReference": func(c *vnode) { r.ublRef(c, "quotation") },
		"cac:ProjectReference":            func(c *vnode) { r.ublRef(c, "project") },
		"cac:AccountingSupplierParty": func(c *vnode) {
			r.apply(c, map[string]func(*vnode){
				"cac:Party": func(d *vnode) {
					r.inv.Seller.Party, r.inv.Seller.TradingName = r.ublParty(d, false)
				},
			})
		},
		"cac:AccountingCustomerParty": func(c *vnode) {
			r.apply(c, map[string]func(*vnode){
				"cac:Party": func(d *vnode) {
					p, _ := r.ublParty(d, false)
					r.inv.Buyer = p
				},
			})
		},
		"cac:PayeeParty": func(c *vnode) {
			// The payee is the one party UBL writes without a cac:Party of its own.
			p, _ := r.ublParty(c, true)
			r.inv.Payee = &p
		},
		"cac:Delivery":     r.ublDelivery,
		"cac:PaymentMeans": r.ublPaymentMeans,
		"cac:PaymentTerms": func(c *vnode) {
			// UBL gives the payment terms one note, so the model's lines of
			// text are split back out of it.
			if s := r.str(c, "cbc:Note"); s != "" {
				r.inv.Terms = strings.Split(s, "\n")
			}
		},
		"cac:AllowanceCharge":    r.ublAC,
		"cac:TaxTotal":           r.ublTaxTotal,
		"cac:LegalMonetaryTotal": r.ublTotals,
		"cac:InvoiceLine":        r.ublLine,
		"cac:CreditNoteLine":     r.ublLine,
	})
	r.unmapped(root)
	if len(r.errs) > 0 {
		return nil, r.errf("UBL")
	}
	return &ReadReport{Invoice: r.inv, Issues: r.issues}, nil
}

// ublOrder reads the order reference. UBL gives the order references one
// element with two slots, and the writer fills the second only for a sales
// order reference, so a document with both slots says so and a document with
// one carries the buyer's own order (BT-13).
func (r *reader) ublOrder(n *vnode) {
	id, sales := r.str(n, "cbc:ID"), r.str(n, "cbc:SalesOrderID")
	if sales == "" {
		if id != "" {
			r.inv.References = append(r.inv.References, Reference{Type: "purchase", ID: id})
		}
		return
	}
	if id != "" {
		r.inv.References = append(r.inv.References, Reference{Type: "buyerOrder", ID: id})
	}
	r.inv.References = append(r.inv.References, Reference{Type: "salesOrder", ID: sales})
}

// ublRef reads a document reference reduced to its identifier. The element's
// name decides what kind of reference it is, which is how the model's types map
// onto UBL's one element per kind.
func (r *reader) ublRef(n *vnode, kind string) {
	r.inv.References = append(r.inv.References, Reference{Type: kind, ID: r.str(n, "cbc:ID")})
}

// ublParty reads a party, and the trading name that rides in a party name
// element next to the legal name. payee selects the payee shape, which the
// schematron keeps short: its name travels in the party name element, and it
// has no address, contact, endpoint or tax registration.
func (r *reader) ublParty(n *vnode, payee bool) (Party, string) {
	p := Party{}
	trading := ""
	if id := r.takeChild(n, "cbc:EndpointID"); id != nil {
		p.EAddress = text(id)
		p.EAScheme = id.attr["schemeID"]
	}
	r.apply(n, map[string]func(*vnode){
		"cac:PartyIdentification": func(c *vnode) {
			if id := r.takeChild(c, "cbc:ID"); id != nil {
				p.Identifier = text(id)
				p.IDScheme = id.attr["schemeID"]
			}
		},
		"cac:PartyName": func(c *vnode) {
			name := r.str(c, "cbc:Name")
			if payee {
				p.Name = name
				return
			}
			trading = name
		},
		"cac:PostalAddress": func(c *vnode) { p.Address = r.ublAddress(c) },
		"cac:PartyTaxScheme": func(c *vnode) {
			if v := r.str(c, "cbc:CompanyID"); v != "" {
				// UBL holds a VAT registration in a tax scheme element, and
				// the model names the scheme VAT.
				p.TaxIDs = append(p.TaxIDs, TaxID{Value: v, Scheme: "VAT"})
			}
			r.ublScheme(r.find(c, "cac:TaxScheme"))
		},
		"cac:PartyLegalEntity": func(c *vnode) {
			// A payee's legal entity element carries only its registration,
			// so the name read from the party name element stays put.
			if name := r.str(c, "cbc:RegistrationName"); name != "" {
				p.Name = name
			}
			if reg := r.str(c, "cbc:CompanyID"); reg != "" {
				p.RegIDs = []string{reg}
			}
		},
		"cac:Contact": func(c *vnode) {
			ct := Contact{
				Name:  r.str(c, "cbc:Name"),
				Phone: r.str(c, "cbc:Telephone"),
				Email: r.str(c, "cbc:ElectronicMail"),
			}
			if ct != (Contact{}) {
				p.Contact = &ct
			}
		},
	})
	return p, trading
}

func (r *reader) ublAddress(n *vnode) Address {
	a := Address{}
	r.into(n, "cbc:StreetName", &a.Line1)
	r.into(n, "cbc:AdditionalStreetName", &a.Line2)
	if l := r.takeChild(n, "cac:AddressLine"); l != nil {
		a.Line3 = r.str(l, "cbc:Line")
	}
	r.into(n, "cbc:CityName", &a.City)
	r.into(n, "cbc:PostalZone", &a.Postal)
	r.into(n, "cbc:CountrySubentity", &a.Zone)
	if c := r.takeChild(n, "cac:Country"); c != nil {
		a.Country = r.str(c, "cbc:IdentificationCode")
	}
	return a
}

// ublScheme accounts for the tax scheme of a category. UBL allows nothing but
// VAT inside it (UBL-CR-484 and its siblings), so a document that names another
// tax carries something the EN 16931 model has no term for.
func (r *reader) ublScheme(n *vnode) {
	if id := r.str(n, "cbc:ID"); id != "" && id != ublTaxSchemeID {
		r.issues = append(r.issues, fmt.Sprintf("%s: a tax type of %s has no place in the EN 16931 model, which knows only VAT", pathOf(n), id))
	}
	r.consume(n)
}

func (r *reader) ublDelivery(n *vnode) {
	d := r.delivery()
	r.apply(n, map[string]func(*vnode){
		"cbc:ActualDeliveryDate": func(c *vnode) { d.ArrivalDate = r.dateOf(c) },
		"cac:DeliveryLocation": func(c *vnode) {
			d.ReceiverID = r.str(c, "cbc:ID")
			if a := r.takeChild(c, "cac:Address"); a != nil {
				d.Address = r.ublAddress(a)
			}
		},
		"cac:DeliveryParty": func(c *vnode) {
			// The deliver to party name (BT-70) rides in a party name.
			for _, nm := range r.children(c, "cac:PartyName") {
				if s := r.str(nm, "cbc:Name"); s != "" {
					d.Name = s
				}
			}
		},
		"cac:PromisedDeliveryPeriod": func(c *vnode) { d.Period = r.period(c) },
	})
}

func (r *reader) ublPaymentMeans(n *vnode) {
	if !r.once(n, "paymentmeans", "payment means block") {
		r.consume(n)
		return
	}
	p := r.payment()
	if code := r.takeChild(n, "cbc:PaymentMeansCode"); code != nil {
		p.Code = text(code)
		p.Name = code.attr["name"]
	}
	r.apply(n, map[string]func(*vnode){
		"cbc:PaymentID": func(c *vnode) { p.RemitInfo = text(c) },
		"cac:CardAccount": func(c *vnode) {
			p.Card = &Card{
				Pan:    r.str(c, "cbc:PrimaryAccountNumberID"),
				Scheme: r.str(c, "cbc:NetworkID"),
				Holder: r.str(c, "cbc:HolderName"),
			}
		},
		"cac:PayeeFinancialAccount": func(c *vnode) {
			a := r.account(c, "cbc:ID", "")
			r.inv.SellerAccount = &a
		},
		// A payer account outside a mandate is beyond EN 16931, and the UBL
		// schematron advises against it (UBL-CR-680), but the model has a
		// field for it and the document did say it.
		"cac:PayerFinancialAccount": func(c *vnode) {
			a := r.account(c, "cbc:ID", "")
			r.inv.BuyerAccount = &a
		},
		"cac:PaymentMandate": func(c *vnode) {
			m := Mandate{ID: r.str(c, "cbc:ID")}
			if b := r.takeChild(c, "cac:PayerFinancialAccount"); b != nil {
				a := r.account(b, "cbc:ID", "")
				m.Bank = &a
			}
			p.Mandate = &m
		},
	})
}

// ublACParts reads the halves an allowance or charge element shares between the
// syntaxes, and says which of the two it was.
func (r *reader) ublACParts(n *vnode) (ac AllowanceCharge, charge bool) {
	charge = r.ind(n, "cbc:ChargeIndicator")
	r.readAC(n, &ac, "cbc:MultiplierFactorNumeric", "cbc:Amount", "cbc:BaseAmount")
	ac.ReasonCode = r.str(n, "cbc:AllowanceChargeReasonCode")
	ac.Reason = r.str(n, "cbc:AllowanceChargeReason")
	return ac, charge
}

func (r *reader) ublAC(n *vnode) {
	a, charge := r.ublACParts(n)
	if t := r.takeChild(n, "cac:TaxCategory"); t != nil {
		a.TaxCategory = r.str(t, "cbc:ID")
		a.TaxRate = r.numOf(t, "cbc:Percent")
		r.ublScheme(r.find(t, "cac:TaxScheme"))
	}
	if charge {
		r.inv.Charges = append(r.inv.Charges, a)
		return
	}
	r.inv.Allowances = append(r.inv.Allowances, a)
}

// ublTaxTotal reads the tax total. UBL carries one total for the document and
// repeats a subtotal per rate, where CII lists both at the same level.
func (r *reader) ublTaxTotal(n *vnode) {
	if !r.once(n, "taxtotal", "tax total") {
		r.consume(n)
		return
	}
	assignNum(r.numOf(n, "cbc:TaxAmount"), &r.inv.Totals.TaxTotal)
	for _, s := range r.children(n, "cac:TaxSubtotal") {
		v := VATRate{}
		if a := r.numOf(s, "cbc:TaxableAmount"); a != nil {
			v.Taxable = *a
		}
		if a := r.numOf(s, "cbc:TaxAmount"); a != nil {
			v.Amount = *a
		}
		if t := r.takeChild(s, "cac:TaxCategory"); t != nil {
			v.Category = r.str(t, "cbc:ID")
			if p := r.numOf(t, "cbc:Percent"); p != nil {
				v.Percent = *p
			}
			// UBL 2.1 has no exemption reason on the subtotal itself, so both
			// halves sit in the category, as the Peppol mapping puts them.
			v.Exempt = r.exempt(t, "cbc:TaxExemptionReason", "cbc:TaxExemptionReasonCode")
			r.ublScheme(r.find(t, "cac:TaxScheme"))
		}
		r.inv.VATBreakdown = append(r.inv.VATBreakdown, v)
	}
}

func (r *reader) ublTotals(n *vnode) {
	t := &r.inv.Totals
	assignNum(r.numOf(n, "cbc:LineExtensionAmount"), &t.LineTotal)
	assignNum(r.numOf(n, "cbc:TaxExclusiveAmount"), &t.TaxExclusive)
	assignNum(r.numOf(n, "cbc:TaxInclusiveAmount"), &t.TaxInclusive)
	assignNum(r.numOf(n, "cbc:AllowanceTotalAmount"), &t.AllowanceTotal)
	assignNum(r.numOf(n, "cbc:ChargeTotalAmount"), &t.ChargeTotal)
	assignNum(r.numOf(n, "cbc:PrepaidAmount"), &r.inv.Prepaid)
	assignNum(r.numOf(n, "cbc:PayableRoundingAmount"), &r.inv.Rounding)
	assignNum(r.numOf(n, "cbc:PayableAmount"), &t.Payable)
}

// ublLine reads one line, of an invoice or of a credit note: the two differ in
// the name of the line and of its quantity.
func (r *reader) ublLine(n *vnode) {
	ln := Line{}
	var pr priceRead
	var notes []string
	qname := "cbc:InvoicedQuantity"
	if n.q == "cac:CreditNoteLine" {
		qname = "cbc:CreditedQuantity"
	}
	r.apply(n, map[string]func(*vnode){
		"cbc:ID":   func(c *vnode) { ln.ID = text(c) },
		"cbc:Note": func(c *vnode) { notes = append(notes, text(c)) },
		qname:      func(c *vnode) { ln.Quantity, ln.UnitCode = r.qty(c) },
		"cbc:LineExtensionAmount": func(c *vnode) {
			v := r.num(c)
			ln.Extension = &v
		},
		"cbc:AccountingCost": func(c *vnode) { ln.Account = &Account{ID: text(c)} },
		"cac:InvoicePeriod":  func(c *vnode) { ln.Period = r.period(c) },
		"cac:AllowanceCharge": func(c *vnode) {
			ac, charge := r.ublACParts(c)
			// UBL-CR-558 warns away a tax category on a line correction for
			// the same reason CII-SR-191 does: it takes the VAT of its line.
			if t := r.find(c, "cac:TaxCategory"); t != nil {
				r.consume(t)
				r.issues = append(r.issues, fmt.Sprintf("%s: a line allowance or charge takes the VAT of its line, so the tax block there is dropped", pathOf(t)))
			}
			if charge {
				ln.Charges = append(ln.Charges, ac)
				return
			}
			ln.Allowances = append(ln.Allowances, ac)
		},
		"cac:Item":  func(c *vnode) { r.ublItem(c, &ln) },
		"cac:Price": func(c *vnode) { pr = r.ublPrice(c, &ln) },
	})
	ln.Note = joinLines(notes)
	// The price is assembled last: whether a base quantity says anything
	// depends on the line's unit code, which sits in the quantity element.
	finishPrice(&ln, pr)
	r.inv.Lines = append(r.inv.Lines, ln)
}

func (r *reader) ublItem(n *vnode, ln *Line) {
	it := &ln.Item
	r.into(n, "cbc:Description", &it.Description)
	r.into(n, "cbc:Name", &it.Name)
	// The identifiers come in the schema's order: the buyer's, the seller's,
	// then the standard one with its scheme.
	if c := r.takeChild(n, "cac:BuyersItemIdentification"); c != nil {
		it.CustomerID = r.str(c, "cbc:ID")
	}
	if c := r.takeChild(n, "cac:SellersItemIdentification"); c != nil {
		it.SellerID = r.str(c, "cbc:ID")
	}
	if c := r.takeChild(n, "cac:StandardItemIdentification"); c != nil {
		if id := r.takeChild(c, "cbc:ID"); id != nil {
			it.StandardID = text(id)
			it.StdScheme = id.attr["schemeID"]
		}
	}
	if c := r.takeChild(n, "cac:OriginCountry"); c != nil {
		it.Origin = r.str(c, "cbc:IdentificationCode")
	}
	for _, c := range r.children(n, "cac:CommodityClassification") {
		code := r.takeChild(c, "cbc:ItemClassificationCode")
		if code == nil {
			continue
		}
		it.ClassIDs = append(it.ClassIDs, text(code))
		if s := code.attr["listID"]; s != "" {
			it.ClassScheme = s
		}
	}
	if t := r.takeChild(n, "cac:ClassifiedTaxCategory"); t != nil {
		ln.TaxCategory = r.str(t, "cbc:ID")
		if p := r.numOf(t, "cbc:Percent"); p != nil {
			ln.TaxRate = *p
		}
		// A line's exemption reason has a UBL element (UBL-CR-600 and 601
		// advise against it, EN 16931 has no such term), and the model does:
		// a document that states one is not asked to repeat itself.
		ln.TaxExempt = r.exempt(t, "cbc:TaxExemptionReason", "cbc:TaxExemptionReasonCode")
		r.ublScheme(r.find(t, "cac:TaxScheme"))
	}
	for _, a := range r.children(n, "cac:AdditionalItemProperty") {
		it.Attributes = append(it.Attributes, Attribute{
			Name:  r.str(a, "cbc:Name"),
			Value: r.str(a, "cbc:Value"),
		})
	}
}

// ublPrice reads the price of a line. UBL has no gross price element, so the
// price discount (BT-147) carries the difference: the price amount is the net,
// and the discount element states the amount off and the gross it was taken
// from.
func (r *reader) ublPrice(n *vnode, ln *Line) priceRead {
	pr := priceRead{}
	ln.NetPrice = amount(r.numOf(n, "cbc:PriceAmount"))
	pr.q, pr.u = r.basisQuantity(n, "cbc:BaseQuantity")
	for _, ac := range r.children(n, "cac:AllowanceCharge") {
		r.take(ac)
		r.ind(ac, "cbc:ChargeIndicator")
		pr.disc = r.numOf(ac, "cbc:Amount")
		pr.gross = r.numOf(ac, "cbc:BaseAmount")
	}
	return pr
}
