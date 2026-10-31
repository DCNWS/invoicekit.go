package einvoice

import (
	"fmt"

	"github.com/dcnws/invoicekit.go/internal/model"
)

// The invoice types below are aliases of the syntax-neutral model, so a caller
// builds one Invoice and asks for either syntax. They are aliases rather than
// wrappers: the JSON tags are the same, and no conversion is needed.

type (
	// Invoice is a syntax-neutral EN 16931 invoice or credit note.
	Invoice = model.Invoice
	// Seller is the supplying party, i.e. a Party plus seller-only fields.
	Seller = model.Seller
	// Party is any party on the invoice: buyer, payee, delivery receiver.
	Party = model.Party
	// Address is a postal address (BG-5, BG-8).
	Address = model.Address
	// Contact is a person to contact (BG-6).
	Contact = model.Contact
	// TaxID is a VAT or other tax registration of a party.
	TaxID = model.TaxID
	// FinancialAcct is a bank account.
	FinancialAcct = model.FinancialAcct
	// Payment describes how and where the invoice is to be paid.
	Payment = model.Payment
	// Card is a card payment means.
	Card = model.Card
	// Mandate is a direct debit mandate.
	Mandate = model.Mandate
	// Delivery describes where and when goods or services were delivered.
	Delivery = model.Delivery
	// Reference points at another document: order, contract, preceding invoice.
	Reference = model.Reference
	// Period is a billing or delivery period.
	Period = model.Period
	// AllowanceCharge is a discount or surcharge, at document or line level.
	AllowanceCharge = model.AllowanceCharge
	// Line is one invoice line.
	Line = model.Line
	// Account is a cost centre or accounting reference.
	Account = model.Account
	// TaxExempt explains why a line is not subject to VAT.
	TaxExempt = model.TaxExempt
	// Item is the goods or services being invoiced.
	Item = model.Item
	// ItemPrice carries price details that do not belong to the line.
	ItemPrice = model.ItemPrice
	// Attribute is a free-form item or line attribute (BG-32).
	Attribute = model.Attribute
	// Totals are the invoice totals; leave them empty to have them computed.
	Totals = model.Totals
	// VATRate is one rate bucket of the VAT breakdown.
	VATRate = model.VATRate
	// Problem is one inconsistency found by Check.
	Problem = model.Problem
)

// Generate renders an invoice as an EN 16931 document in the given syntax.
//
// Missing totals are computed from the lines, the document level allowances and
// charges, and the VAT breakdown, using the arithmetic required by EN 16931
// (BR-CO-10 to BR-CO-15). Supplied totals are emitted as given, so a document
// can be produced with deliberately wrong sums in order to test a validator.
//
// A returned document satisfies the business rules of EN 16931 for a complete
// invoice; the set of fields actually filled in decides how much of the norm is
// exercised. Fields with no agreed representation in the target syntax are
// reported as errors rather than silently dropped.
func Generate(sx Syntax, inv Invoice) ([]byte, error) {
	switch sx {
	case SyntaxCII:
		return model.RenderCII(inv)
	case SyntaxUBL:
		return model.RenderUBL(inv)
	default:
		return nil, fmt.Errorf("einvoice: unknown syntax %q, want %q or %q", sx, SyntaxCII, SyntaxUBL)
	}
}

// GenerateCII renders an invoice as a UN/CEFACT Cross Industry Invoice, the
// syntax carried inside Factur-X and ZUGFeRD and used by XRechnung.
func GenerateCII(inv Invoice) ([]byte, error) { return model.RenderCII(inv) }

// GenerateUBL renders an invoice or credit note as an OASIS UBL document, the
// syntax used by Peppol. Credit note type codes produce a CreditNote rather
// than an Invoice, because the two document types do not share code lists.
func GenerateUBL(inv Invoice) ([]byte, error) { return model.RenderUBL(inv) }

// Check reports inconsistencies inside an invoice model before it is rendered,
// such as supplied totals that disagree with the lines or a line quantity that
// cannot be priced. It is advisory: rendering does not depend on its verdict,
// and the returned slice is empty for a coherent invoice.
func Check(inv Invoice) []Problem { return inv.Check() }

// documentSpec is the pair of header identifiers a rule set expects a document
// to declare about itself: the business process type (BT-23) and the
// specification identifier (BT-24).
type documentSpec struct {
	process   string
	guideline string
}

// specByProfile and specByCIUS give the identifiers each rule set is validated
// against. They are read out of the rule sets themselves: Peppol matches BT-24
// against a prefix (PEPPOL-EN16931-R004) and insists BT-23 is one it recognises
// (-R001, -R007), and XRechnung asks for its own identifier in BR-DE-21. A
// document that names anything else is refused before a single line is read, so
// these are not decoration.
var specByProfile = map[Profile]documentSpec{
	ProfilePeppol: {
		process:   "urn:fdc:peppol.eu:2017:poacc:billing:01:1.0",
		guideline: "urn:cen.eu:en16931:2017#compliant#urn:fdc:peppol.eu:2017:poacc:billing:3.0",
	},
}

var specByCIUS = map[CIUS]documentSpec{
	CIUSXRechnung: {
		guideline: "urn:cen.eu:en16931:2017#compliant#urn:xeinkauf.de:kosit:xrechnung_3.0",
	},
}

// ApplySpec stamps the header identifiers of the rule set named in opts onto an
// invoice, so a generated document declares the specification it is then
// checked against: the business process type (BT-23) and the specification
// identifier (BT-24). Pass the same Options to ApplySpec and to Validate and
// the two halves of the job agree; an empty opts leaves the plain norm's
// identifiers in place. A profile outranks a CIUS, as it does when validating.
//
// XRechnung asks for a Leitweg-ID in BT-10 as well, which no generator can
// invent: set Invoice.BuyerReference.
func ApplySpec(in *Invoice, opts Options) error {
	if err := CheckRuleSets(opts); err != nil {
		return err
	}
	if spec, ok := specByProfile[opts.Profile]; ok {
		in.ProcessType, in.Guideline = spec.process, spec.guideline
		return nil
	}
	if spec, ok := specByCIUS[opts.CIUS]; ok {
		in.ProcessType, in.Guideline = spec.process, spec.guideline
		return nil
	}
	return nil
}
