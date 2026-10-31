// Package en16931 validates and inspects electronic invoices against the
// European norm EN 16931, using the official Schematron rule sets for the two
// standard syntaxes: CI/UN-CEFACT (CII, used by Factur-X and XRechnung) and
// OASIS UBL (used by Peppol BIS). Everything runs in pure Go: no Java, no
// XSLT processor, no external services.
package en16931

import (
	"bytes"
	"embed"
	"encoding/xml"
	"fmt"
	"io"
	"sync"

	"github.com/dcnws/invoicekit.go/internal/sch"
	"github.com/dcnws/invoicekit.go/internal/xp"
	"github.com/dcnws/invoicekit.go/internal/xsd"
)

//go:embed schemas/en16931-cii.sch schemas/en16931-ubl.sch
var schemaFS embed.FS

// Syntax identifies the invoice syntax.
type Syntax string

const (
	// SyntaxCII is the UN/CEFACT Cross Industry Invoice syntax (Factur-X,
	// ZUGFeRD, XRechnung).
	SyntaxCII Syntax = "CII"
	// SyntaxUBL is the OASIS UBL Invoice / CreditNote syntax (Peppol BIS).
	SyntaxUBL Syntax = "UBL"
)

const (
	nsCII  = "urn:un:unece:uncefact:data:standard:CrossIndustryInvoice:100"
	nsUBL  = "urn:oasis:names:specification:ubl:schema:xsd:Invoice-2"
	nsUBLC = "urn:oasis:names:specification:ubl:schema:xsd:CreditNote-2"
)

// schemaFile maps a syntax to its embedded Schematron file.
var schemaFile = map[Syntax]string{
	SyntaxCII: "schemas/en16931-cii.sch",
	SyntaxUBL: "schemas/en16931-ubl.sch",
}

var (
	cacheMu sync.Mutex
	cache   = map[string]*sch.Schema{}
)

// cached parses and compiles a rule set at most once per key: rule sets are
// static data, and compiling a few hundred assertions costs more than the
// document validation itself.
func cached(key string, load func() (*sch.Schema, error)) (*sch.Schema, error) {
	cacheMu.Lock()
	defer cacheMu.Unlock()
	if s, ok := cache[key]; ok {
		return s, nil
	}
	s, err := load()
	if err != nil {
		return nil, err
	}
	cache[key] = s
	return s, nil
}

// Schematron returns the parsed and compiled rule set for a syntax. The result
// is cached, so validating many documents is cheap.
func Schematron(sx Syntax) (*sch.Schema, error) {
	name, ok := schemaFile[sx]
	if !ok {
		return nil, fmt.Errorf("en16931: unknown syntax %q (want CII or UBL)", sx)
	}
	return cached("en16931|"+string(sx), func() (*sch.Schema, error) {
		data, err := schemaFS.ReadFile(name)
		if err != nil {
			return nil, err
		}
		s, err := sch.Parse(data)
		if err != nil {
			return nil, fmt.Errorf("en16931: %s schema: %w", sx, err)
		}
		s.Name = "en16931"
		if err := s.Compile(); err != nil {
			return nil, fmt.Errorf("en16931: %s schema: %w", sx, err)
		}
		return s, nil
	})
}

// Detect returns the syntax of an invoice document.
func Detect(data []byte) (Syntax, error) {
	dec := xml.NewDecoder(bytes.NewReader(data))
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			return "", fmt.Errorf("no XML root element found: input is not an invoice document")
		}
		if err != nil {
			return "", fmt.Errorf("en16931: %w", err)
		}
		se, ok := tok.(xml.StartElement)
		if !ok {
			continue
		}
		switch {
		case se.Name.Local == "CrossIndustryInvoice":
			return SyntaxCII, nil
		case (se.Name.Local == "Invoice" || se.Name.Local == "CreditNote") &&
			(se.Name.Space == nsUBL || se.Name.Space == nsUBLC || se.Name.Space == "ubl"):
			return SyntaxUBL, nil
		}
		return "", fmt.Errorf("en16931: %s is not an EN 16931 invoice root element", qname(se.Name))
	}
}

func qname(n xml.Name) string {
	if n.Space != "" {
		return "{" + n.Space + "}" + n.Local
	}
	return n.Local
}

// Report is the outcome of validating one document.
type Report struct {
	Syntax Syntax
	// CIUS is the extra rule set that ran, empty when only EN 16931 was checked.
	CIUS CIUS
	// Profile is the rule-set bundle that ran, empty when the EN 16931
	// artefacts themselves were used. A profile carries its own copy of the
	// norm, so its presence means the base artefacts did not run.
	Profile  Profile
	Findings []sch.Finding

	// Schema lists structural problems found against the XML schema of the
	// syntax: an element in a place its schema does not allow, a missing
	// mandatory child, repeated content, text where elements are expected.
	// The Schematron rule sets never check this, so the two lists answer
	// different questions and are kept apart.
	Schema []xsd.Finding

	// Fatal counts findings that are business rule breaches (flag fatal);
	// Warn counts the rest.
	Fatal int
	Warn  int

	// Internal counts findings produced by the validator itself rather than by
	// the document, i.e. rules that could not be evaluated.
	Internal int
}

// Valid reports whether the document breaks no business rule and matches the
// XML schema of its syntax.
func (r *Report) Valid() bool {
	return r.Fatal == 0 && r.Warn == 0 && r.Internal == 0 && len(r.Schema) == 0
}

// IDs returns the distinct rule ids that fired.
func (r *Report) IDs() []string {
	seen := map[string]bool{}
	var out []string
	for _, f := range r.Findings {
		if f.ID != "" && !seen[f.ID] {
			seen[f.ID] = true
			out = append(out, f.ID)
		}
	}
	return out
}

// Validate parses data and checks it against the EN 16931 rules. The syntax is
// detected from the root element unless one is given. Pass phase ids to restrict
// which rules run; by default every phase runs (model, code list and syntax
// checks).
func Validate(data []byte, opts Options) (*Report, error) {
	sx := opts.Syntax
	if sx == "" {
		var err error
		if sx, err = Detect(data); err != nil {
			return nil, err
		}
	}
	doc, err := xp.ParseXML(string(data))
	if err != nil {
		return nil, err
	}
	return validateDoc(sx, doc, opts.Phases, opts.CIUS, opts.Profile)
}

// ValidateDoc validates an already parsed document against EN 16931.
func ValidateDoc(sx Syntax, doc *xp.Doc, phases []string) (*Report, error) {
	return validateDoc(sx, doc, phases, "", "")
}

// ValidateDocCIUS validates an already parsed document against EN 16931 plus a
// CIUS rule set such as XRechnung.
func ValidateDocCIUS(sx Syntax, doc *xp.Doc, phases []string, cius CIUS) (*Report, error) {
	return validateDoc(sx, doc, phases, cius, "")
}

// ValidateDocWith validates an already parsed document with the rule sets and
// phases an Options asks for. Options.Syntax is ignored: the syntax of the
// document is the one given here.
func ValidateDocWith(sx Syntax, doc *xp.Doc, opts Options) (*Report, error) {
	return validateDoc(sx, doc, opts.Phases, opts.CIUS, opts.Profile)
}

// ruleSets returns the rule sets a run needs, in the order they should run. A
// profile brings its own copy of the EN 16931 assertions and therefore
// replaces the base artefacts; a CIUS never does, so it always runs on top of
// whatever the base is.
func ruleSets(sx Syntax, cius CIUS, profile Profile) ([]*sch.Schema, error) {
	var sets []*sch.Schema
	if profile != "" {
		ps, err := ProfileRuleSets(profile, sx)
		if err != nil {
			return nil, err
		}
		sets = ps
	} else {
		base, err := Schematron(sx)
		if err != nil {
			return nil, err
		}
		sets = []*sch.Schema{base}
	}
	if cius == "" {
		return sets, nil
	}
	c, err := CiusSchematron(cius, sx)
	if err != nil {
		return nil, err
	}
	return append(sets, c), nil
}

func validateDoc(sx Syntax, doc *xp.Doc, phases []string, cius CIUS, profile Profile) (*Report, error) {
	sets, err := ruleSets(sx, cius, profile)
	if err != nil {
		return nil, err
	}
	var fs []sch.Finding
	for i, s := range sets {
		p := phases
		if i > 0 {
			p = nil
		}
		more, err := s.Validate(doc, p)
		if err != nil {
			return nil, err
		}
		fs = append(fs, more...)
	}
	r := &Report{Syntax: sx, CIUS: cius, Profile: profile, Findings: fs}
	if sh, err := SchemaFindings(sx, doc); err != nil {
		return nil, err
	} else {
		r.Schema = sh
	}
	for _, f := range fs {
		switch {
		case f.Err != "":
			r.Internal++
		case f.Flag == "fatal" || f.Flag == "":
			r.Fatal++
		default:
			r.Warn++
		}
	}
	return r, nil
}

// Options controls a validation run.
type Options struct {
	// Syntax forces a syntax; when empty it is detected from the document.
	Syntax Syntax
	// Phases restricts the rule phases (all phases when empty). It applies to
	// the EN 16931 rule set; a CIUS rule set always runs its own default phases.
	Phases []string
	// CIUS adds a national or network rule set on top of EN 16931, for example
	// CIUSXRechnung. Empty checks the norm alone.
	CIUS CIUS
	// Profile selects a published rule-set bundle such as ProfilePeppol, which
	// carries its own copy of the EN 16931 artefacts and therefore replaces the
	// ones this library runs by default. ProfileNames() lists what is
	// available. A CIUS still runs on top of it.
	Profile Profile
}
