package einvoice

import "github.com/dcnws/invoicekit.go/internal/en16931"

// Artefact describes the release of the official rule-set files this library
// embeds for one syntax. The European Commission stamps a version and a
// last-update date into each Schematron it publishes, so a verdict can always
// be traced back to the artefacts that produced it.
type Artefact struct {
	// Syntax is the syntax whose rule set is described.
	Syntax Syntax `json:"syntax"`
	// Version is the release of the rule set, as stamped by the Commission.
	Version string `json:"version"`
	// Updated is the last-update date of the rule set, YYYY-MM-DD.
	Updated string `json:"updated"`
}

// Artefacts lists one entry per syntax, in a stable order, naming the release
// of the embedded rule set behind the verdicts for that syntax.
func Artefacts() []Artefact {
	out := make([]Artefact, 0, 2)
	for _, sx := range []Syntax{SyntaxCII, SyntaxUBL} {
		version, updated := en16931.ArtefactInfo(en16931.Syntax(sx))
		out = append(out, Artefact{Syntax: sx, Version: version, Updated: updated})
	}
	return out
}
