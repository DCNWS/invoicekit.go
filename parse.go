package einvoice

import (
	"fmt"

	"github.com/dcnws/invoicekit.go/internal/model"
)

// ParseResult is a document as this library understood it: the invoice it
// carries, the syntax it was written in, and the parts of it that the model has
// no room for.
type ParseResult struct {
	// Invoice is the document translated into the syntax-neutral model. Its
	// totals are the ones the document states, not recomputed ones, so what
	// came back is what the sender wrote even when that is wrong. Call
	// Validate over the same bytes for the arithmetic, or Invoice.Derive to
	// have the model work the sums out for itself.
	Invoice Invoice `json:"invoice"`
	// Syntax is the concrete syntax the document was read as.
	Syntax Syntax `json:"syntax"`
	// Issues names whatever the document said that the EN 16931 model has no
	// term for — a tax type that is not VAT, an element from a schema this
	// library does not read, a second copy of something the model holds once.
	// Each one says where in the document it was found. It is never empty by
	// accident: a document that fits the model completely comes back with no
	// issues, and one that does not is still returned as an invoice.
	Issues []string `json:"issues"`
}

// Parse reads an EN 16931 invoice or credit note in either syntax into the
// model. It answers a different question from Validate: Validate says whether a
// document obeys the rules, Parse says what the document says. A document that
// breaks rules parses as an invoice carrying exactly the amounts it states,
// wrong ones included, which is why the two are usually run over the same bytes
// rather than one after the other.
//
// An error means the input is not an invoice document at all: not XML, a root
// element that is not an invoice, or XML that stops partway through. Anything
// that is an invoice comes back as one, with whatever had nowhere to go listed
// in ParseResult.Issues.
func Parse(data []byte) (*ParseResult, error) {
	sx, err := Detect(data)
	if err != nil {
		return nil, err
	}
	switch sx {
	case SyntaxCII:
		return ParseCII(data)
	case SyntaxUBL:
		return ParseUBL(data)
	default:
		return nil, fmt.Errorf("einvoice: cannot read: %q is not a syntax this library reads", sx)
	}
}

// ParseCII reads a UN/CEFACT Cross Industry Invoice — the syntax inside
// Factur-X and ZUGFeRD, and the one XRechnung is written in.
func ParseCII(data []byte) (*ParseResult, error) {
	rep, err := model.ReadCII(data)
	if err != nil {
		return nil, err
	}
	return newResult(rep.Invoice, SyntaxCII, rep.Issues), nil
}

// ParseUBL reads an OASIS UBL Invoice or CreditNote, the syntax Peppol carries.
// The document type is not chosen in advance: a CreditNote root reads as a
// credit note in the same model an Invoice does.
func ParseUBL(data []byte) (*ParseResult, error) {
	rep, err := model.ReadUBL(data)
	if err != nil {
		return nil, err
	}
	return newResult(rep.Invoice, SyntaxUBL, rep.Issues), nil
}

// newResult builds the result. A reader that found nothing to report returns a
// nil issue list, which encodes as null in JSON; a page then has to test for two
// kinds of "nothing found", so the list is always an array here.
func newResult(inv model.Invoice, sx Syntax, issues []string) *ParseResult {
	if issues == nil {
		issues = []string{}
	}
	return &ParseResult{Invoice: inv, Syntax: sx, Issues: issues}
}
