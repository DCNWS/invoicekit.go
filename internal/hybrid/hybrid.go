// Package hybrid checks a Factur-X / ZUGFeRD document the way a recipient's
// software does: the PDF envelope first, then the invoice carried inside it,
// against whichever rule set the caller asks for.
//
// The two halves come from packages that cannot see each other. internal/facturx
// sits below the rule engine and must not import it — reading a PDF attachment
// needs no idea what an invoice line is — so the pairing lives here, at the top,
// where both are ordinary dependencies.
package hybrid

import (
	"fmt"

	"github.com/dcnws/invoicekit.go"
	"github.com/dcnws/invoicekit.go/internal/facturx"
)

// Result is one hybrid document's two verdicts. The envelope report is embedded,
// so the JSON the CLI and the browser build emit keeps the shape they already
// have, with a rules block added beside it.
type Result struct {
	facturx.CheckResult

	// Rules is the business-rule verdict on the invoice XML inside the PDF. It
	// is nil when the document carries no XML at all, or when the rule engine
	// could not read what it does carry; in that second case the envelope
	// findings say so under the fx.rules id, so a caller never has to guess
	// why the block is missing.
	Rules *einvoice.Result `json:"rules,omitempty"`
}

// Check audits the envelope of a hybrid PDF and validates the invoice inside it,
// in one pass over the file: the payload is the one Inspect already decompressed
// for the envelope checks, so a large PDF is parsed once regardless of how many
// rule sets run.
//
// The opt rule set applies to the payload alone — EN 16931 by default, plus a
// national CIUS or a published bundle such as Peppol BIS when asked for. The
// envelope verdict does not move with it: an acceptable Factur-X envelope can
// still carry an invoice that breaks BR-CO-15, and a recipient needs to know
// which of the two they have to fix.
func Check(data []byte, opt einvoice.Options) (Result, error) {
	if err := CheckOptions(opt); err != nil {
		return Result{}, err
	}
	doc, rep, err := facturx.Audit(data)
	if err != nil {
		return Result{}, err
	}
	// The rules have to run before the envelope result is built: a payload the
	// engine cannot read adds a finding to rep, and Result copies the findings
	// it was handed.
	rules := rulesInside(rep, doc.XML, opt)
	return Result{CheckResult: facturx.Result(doc, rep), Rules: rules}, nil
}

// CheckOptions reports a rule set the caller asked for that this build does not
// carry. It is settled before a byte of the document is read: a misspelt name is
// the caller's mistake, and reporting it among the findings would send someone
// off to repair a PDF that was never the problem.
func CheckOptions(opt einvoice.Options) error { return einvoice.CheckRuleSets(opt) }

// rulesInside runs the engine over the payload. No payload means no opinion: a
// plain PDF gets an envelope verdict and nothing else. A payload the engine
// cannot parse becomes one envelope finding, not a fabricated rule report — the
// rules never ran, and saying so is the honest answer.
func rulesInside(rep *facturx.Report, xml []byte, opt einvoice.Options) *einvoice.Result {
	if len(xml) == 0 {
		return nil
	}
	rules, err := einvoice.Validate(xml, opt)
	if err != nil {
		rep.Findings = append(rep.Findings, facturx.Finding{
			Severity: facturx.SeverityWarning,
			Rule:     "fx.rules",
			Message:  fmt.Sprintf("the invoice XML inside could not be read as an EN 16931 invoice, so no business rules ran: %v", err),
		})
		return nil
	}
	return rules
}
