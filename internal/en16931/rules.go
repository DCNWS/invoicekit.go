package en16931

import (
	"github.com/dcnws/invoicekit.go/internal/sch"
)

// Rule is one assertion a build carries, exactly as published: its id, the rule
// set that states it, what it says is wrong, and the phases that activate it.
// It is the read side of the engine — the list a caller publishes, counts or
// diffs against the next release — and it answers that question without a
// document to validate.
type Rule struct {
	// ID is the published rule id, for example BR-21, CII-SR-009 or BR-DE-16.
	ID string
	// Source names the rule set that states it: en16931, xrechnung, peppol-cen,
	// peppol. Findings carry the same name, so a listed rule can be matched to
	// the report that raised it.
	Source string
	// Flag is the severity: fatal, or warning for a soft rule. Schematron's
	// default of fatal is spelled out rather than left empty.
	Flag string
	// Text is the assertion message as published, its [BR-21] prefix included.
	Text string
	// Context is the XPath the assertion hangs off, and Test the XPath that
	// must hold. Together they say which part of a document a rule looks at.
	Context string
	Test    string
	// Phases lists the phase ids that activate the rule. Empty means the rule
	// set declares no phases, so the rule always runs.
	Phases []string
}

// Rules lists every assertion a validation run would apply for a syntax with the
// given CIUS and profile, in the order the rule sets run and, within each, in
// document order. The composition is the one Validate uses: a profile carries
// its own copy of the norm and so replaces the base artefacts, while a CIUS is
// always added on top.
func Rules(sx Syntax, cius CIUS, profile Profile) ([]Rule, error) {
	sets, err := ruleSets(sx, cius, profile)
	if err != nil {
		return nil, err
	}
	var out []Rule
	for _, s := range sets {
		out = append(out, listRules(s)...)
	}
	return out, nil
}

// listRules walks one parsed rule set.
func listRules(s *sch.Schema) []Rule {
	phases := phasesByPattern(s)
	var out []Rule
	for _, p := range s.Patterns {
		for _, r := range p.Rules {
			for _, a := range r.Asserts {
				if a.Report {
					continue // <report> fires when its test is true: not a check to satisfy
				}
				out = append(out, Rule{
					ID:      a.ID,
					Source:  s.Name,
					Flag:    flagName(a.Flag),
					Text:    a.Message,
					Context: r.Context,
					Test:    a.Test,
					Phases:  phases[p.ID],
				})
			}
		}
	}
	return out
}

// phasesByPattern maps each pattern to the phases that activate it, which is how
// a rule knows which half of the norm it belongs to.
func phasesByPattern(s *sch.Schema) map[string][]string {
	out := map[string][]string{}
	for _, ph := range s.Phases {
		for _, id := range ph.Patterns {
			out[id] = append(out[id], ph.ID)
		}
	}
	return out
}

// flagName spells out the severity a finding would carry. An empty flag means
// Schematron's default, which validateDoc also counts as fatal.
func flagName(flag string) string {
	if flag == "" {
		return "fatal"
	}
	return flag
}
