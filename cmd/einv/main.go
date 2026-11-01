// Command einv validates EN 16931 electronic invoices (UN/CEFACT CII and
// OASIS UBL) against the official Schematron business rules, in pure Go with
// no external processes.
//
//	einv validate [-json] <file|->...   validate documents (exit 1 if invalid)
//	einv gen [-syntax S] [-o out] [-cius NAME] [-profile NAME] <file|->
//	                       render an invoice model as CII or UBL XML
//	einv parse [-o out] <file>     read a document as an invoice model (JSON)
//	einv detect <file>                  report the detected invoice syntax
//	einv phases [cii|ubl]               list the available rule phases
//	einv cius [name]                    list CIUS rule sets and their phases
//	einv profile [name]                 list rule-set bundles and their phases
//	einv embed [-o out.pdf] <pdf> <xml> wrap a PDF and an XML as Factur-X
//	einv extract [-o out.xml] <pdf>     pull the invoice XML out of a PDF
//	einv check [-json] <file|->...      audit hybrid PDFs for PDF/A-3 defects
package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"time"

	"github.com/dcnws/invoicekit.go"
	"github.com/dcnws/invoicekit.go/facturx"
	"github.com/dcnws/invoicekit.go/hybrid"
)

// version is the library release, so `einv version` and the verdicts it
// prints always name the same artefacts.
const version = einvoice.Version

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	var err error
	switch os.Args[1] {
	case "validate", "v":
		err = runValidate(os.Args[2:])
	case "gen", "g":
		err = runGen(os.Args[2:])
	case "parse", "read":
		err = runParse(os.Args[2:])
	case "detect", "d":
		err = runDetect(os.Args[2:])
	case "phases", "p":
		err = runPhases(os.Args[2:])
	case "rules", "list":
		err = runRules(os.Args[2:])
	case "cius":
		err = runCius(os.Args[2:])
	case "profile":
		err = runProfile(os.Args[2:])
	case "embed", "e":
		err = runEmbed(os.Args[2:])
	case "extract", "x":
		err = runExtract(os.Args[2:])
	case "check", "c":
		err = runCheck(os.Args[2:])
	case "-h", "--help", "help":
		usage()
		return
	case "-V", "--version":
		fmt.Println("einv " + version)
		return
	default:
		fmt.Fprintf(os.Stderr, "einv: unknown command %q\n", os.Args[1])
		usage()
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "einv:", err)
		os.Exit(2)
	}
}

func usage() {
	fmt.Fprint(os.Stderr, `einv `+version+` - EN 16931 invoice validation (CII and UBL), pure Go

Usage:
  einv validate [-json] [-max N] [-syntax cii|ubl] [-phases a,b]
                                              [-cius NAME] [-profile NAME]
                                              <file|->...  validate documents
  einv gen [-syntax S] [-o out] [-cius NAME] [-profile NAME] <file|->
                                              render an invoice model (JSON)
  einv parse [-o out] <file|->                read a document as an invoice
                                              model (JSON)
  einv detect <file>                          detect the invoice syntax
  einv phases [cii|ubl]                       list rule phase ids
  einv rules [-json] [-syntax cii|ubl] [-phase ID] [-cius NAME] [-profile NAME]
                                              list every assertion this build
                                              enforces, as published
  einv cius [name]                            list CIUS rule sets, or the
                                              phases of one of them
  einv profile [name]                         list rule-set bundles (Peppol
                                              BIS), or the phases of one
  einv embed [flags] <pdf> <xml>              write a Factur-X / Order-X PDF
  einv extract [-o out.xml] [-info] <pdf>     pull the invoice XML out of a PDF
  einv check [-json] [-max N] [-cius NAME] [-profile NAME] <file|->...
                                              audit a hybrid PDF and the invoice inside

The hybrid commands need no external tools: the PDF is parsed and rewritten
directly. einv check reports what a validator would say about a PDF that is
already on disk.

Exit status: 0 all documents valid, 1 at least one invalid, 2 usage or I/O error.
`)
}

// ruleSetOf turns the three rule-set flags into Options. Both commands that
// take them share this, so `gen -profile peppol` and `validate -profile peppol`
// cannot drift apart into two different meanings of the same flag.
func ruleSetOf(phases, cius, prof string) (einvoice.Options, error) {
	opt := einvoice.Options{}
	for _, p := range strings.Split(phases, ",") {
		if p = strings.TrimSpace(p); p != "" {
			opt.Phases = append(opt.Phases, p)
		}
	}
	if cius != "" {
		opt.CIUS = einvoice.CIUS(strings.ToLower(strings.TrimSpace(cius)))
	}
	if prof != "" {
		opt.Profile = einvoice.Profile(strings.ToLower(strings.TrimSpace(prof)))
	}
	if err := einvoice.CheckRuleSets(opt); err != nil {
		return einvoice.Options{}, err
	}
	return opt, nil
}

func runValidate(args []string) error {
	fs := flag.NewFlagSet("validate", flag.ExitOnError)
	var (
		asJSON = fs.Bool("json", false, "machine-readable JSON output")
		max    = fs.Int("max", 40, "maximum findings to report per document (-1 for all)")
		phases = fs.String("phases", "", "comma-separated phase ids (default: the default phases)")
		syntax = fs.String("syntax", "", "force syntax: cii or ubl (default: auto-detect)")
		cius   = fs.String("cius", "", "also check a country or network rule set on top of EN 16931, e.g. xrechnung (see: einv cius)")
		prof   = fs.String("profile", "", "check a published rule-set bundle instead of our own EN 16931 artefacts, e.g. peppol (see: einv profile)")
	)
	_ = fs.Parse(args)
	paths := fs.Args()
	if len(paths) == 0 {
		return fmt.Errorf("validate: no input files (use - for stdin)")
	}

	sx := einvoice.Syntax("")
	if *syntax != "" {
		var err error
		if sx, err = syntaxOf(*syntax); err != nil {
			return err
		}
	}
	opt, err := ruleSetOf(*phases, *cius, *prof)
	if err != nil {
		return err
	}
	opt.Syntax = sx

	type entry struct {
		Name      string                   `json:"name"`
		Syntax    einvoice.Syntax          `json:"syntax,omitempty"`
		CIUS      einvoice.CIUS            `json:"cius,omitempty"`
		Profile   einvoice.Profile         `json:"profile,omitempty"`
		Valid     bool                     `json:"valid"`
		Fatal     int                      `json:"fatal"`
		Warn      int                      `json:"warn"`
		Counts    map[string]int           `json:"counts,omitempty"`
		Findings  []einvoice.Finding       `json:"findings,omitempty"`
		Schema    []einvoice.SchemaFinding `json:"schema,omitempty"`
		Truncated int                      `json:"truncated,omitempty"`
		Error     string                   `json:"error,omitempty"`
	}

	all := make([]entry, 0, len(paths))
	anyInvalid := false
	anyError := false
	for _, path := range paths {
		data, err := readInput(path)
		if err != nil {
			all = append(all, entry{Name: path, Error: err.Error()})
			anyError = true
			continue
		}
		res, err := einvoice.Validate(data, opt)
		if err != nil {
			all = append(all, entry{Name: path, Error: err.Error()})
			anyError = true
			continue
		}
		e := entry{Name: path, Syntax: res.Syntax, CIUS: res.CIUS, Profile: res.Profile,
			Valid: res.Valid, Fatal: res.Fatal, Warn: res.Warn}
		if len(res.Schema) > 0 {
			e.Schema = res.Schema
		}
		if len(res.Findings) > 0 {
			e.Counts = res.Counts()
			e.Findings = res.Findings
			if *max >= 0 && len(e.Findings) > *max {
				e.Truncated = len(e.Findings) - *max
				e.Findings = e.Findings[:*max]
			}
		}
		if !e.Valid {
			anyInvalid = true
		}
		all = append(all, e)
	}

	if *asJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(all); err != nil {
			return err
		}
	} else {
		for _, e := range all {
			if e.Error != "" {
				fmt.Printf("%s: ERROR: %s\n", e.Name, e.Error)
				continue
			}
			profile := ""
			if e.Profile != "" {
				profile = " " + string(e.Profile)
			}
			if e.CIUS != "" {
				profile += " + " + string(e.CIUS)
			}
			if e.Valid {
				fmt.Printf("%s: valid %s%s document\n", e.Name, e.Syntax, profile)
				continue
			}
			fmt.Printf("%s: INVALID %s%s invoice (%d fatal, %d warning, %d structural)\n",
				e.Name, e.Syntax, profile, e.Fatal, e.Warn, len(e.Schema))
			for _, f := range e.Schema {
				fmt.Printf("  %-12s %s at %s\n", "SCHEMA", f.Message, f.Location)
			}
			for _, f := range e.Findings {
				loc := ""
				if f.Context != "" {
					loc = " at " + f.Context
				}
				fmt.Printf("  %-12s %s%s\n", f.ID, f.Message, loc)
			}
			if e.Truncated > 0 {
				fmt.Printf("  ... and %d more (use -max -1 to list all)\n", e.Truncated)
			}
		}
	}
	switch {
	case anyInvalid:
		os.Exit(1)
	case anyError:
		os.Exit(2)
	}
	return nil
}

// runGen turns an invoice model held as JSON into a document. The model is
// documented by the JSON tags of einvoice.Invoice, so the same file can feed
// both syntaxes; totals left out are computed by the generator.
func runGen(args []string) error {
	fs := flag.NewFlagSet("gen", flag.ExitOnError)
	var (
		syn    = fs.String("syntax", "ubl", "syntax to render: cii or ubl")
		out    = fs.String("o", "", "output file (default: stdout)")
		noVet  = fs.Bool("no-validate", false, "skip validating the generated document")
		phases = fs.String("phases", "", "comma-separated phase ids (default: the default phases)")
		cius   = fs.String("cius", "", "render for a country or network rule set and check it, e.g. xrechnung (see: einv cius)")
		prof   = fs.String("profile", "", "render for a rule-set bundle and check it, e.g. peppol (see: einv profile)")
	)
	_ = fs.Parse(args)
	if fs.NArg() != 1 {
		return fmt.Errorf("gen: expected exactly one invoice model file (use - for stdin)")
	}
	sx, err := syntaxOf(*syn)
	if err != nil {
		return err
	}
	data, err := readInput(fs.Arg(0))
	if err != nil {
		return err
	}
	inv, err := einvoice.DecodeInvoiceModel(bytes.NewReader(data))
	if err != nil {
		return err
	}
	opt, err := ruleSetOf(*phases, *cius, *prof)
	if err != nil {
		return err
	}
	opt.Syntax = sx
	// The document has to say what it is, or the rule set it aims at will never
	// accept it: Peppol reads BT-23 and BT-24 before it reads a single line.
	if err := einvoice.ApplySpec(&inv, opt); err != nil {
		return err
	}
	for _, p := range einvoice.Check(inv) {
		fmt.Fprintf(os.Stderr, "einv: model: %v\n", p)
	}
	doc, err := einvoice.Generate(sx, inv)
	if err != nil {
		return err
	}
	if *out == "" || *out == "-" {
		if _, err := os.Stdout.Write(doc); err != nil {
			return err
		}
	} else if err := os.WriteFile(*out, doc, 0o644); err != nil {
		return err
	}
	if *noVet {
		return nil
	}
	res, err := einvoice.Validate(doc, opt)
	if err != nil {
		return fmt.Errorf("gen: the generated document does not parse: %w", err)
	}
	name := *out
	if name == "" || name == "-" {
		name = "stdout"
	}
	if res.Fatal > 0 || res.Internal > 0 || len(res.Schema) > 0 {
		fmt.Fprintf(os.Stderr, "einv: %s: %d fatal, %d warning, %d unevaluable, %d structural\n",
			name, res.Fatal, res.Warn, res.Internal, len(res.Schema))
		for _, f := range res.Schema {
			fmt.Fprintf(os.Stderr, "  %-12s %s at %s\n", "SCHEMA", f.Message, f.Location)
		}
		for _, f := range res.Findings {
			if f.Flag == "fatal" || f.Err != "" {
				fmt.Fprintf(os.Stderr, "  %-12s %s at %s\n", f.ID, f.Message, f.Context)
			}
		}
		os.Exit(1)
	}
	fmt.Fprintf(os.Stderr, "einv: %s: %s document, %d advisory\n", name, sx, res.Warn)
	return nil
}

// runParse turns a document back into an invoice model: gen seen from the other
// end, so what a customer sends in can become what a generator takes. What the
// document says that the model has no room for is reported on stderr, because a
// model that quietly lost a party's tax number is a worse surprise three
// systems down.
func runParse(args []string) error {
	fs := flag.NewFlagSet("parse", flag.ExitOnError)
	out := fs.String("o", "", "output file for the model as JSON (default: stdout)")
	_ = fs.Parse(args)
	if fs.NArg() != 1 {
		return fmt.Errorf("parse: expected exactly one document file (use - for stdin)")
	}
	data, err := readInput(fs.Arg(0))
	if err != nil {
		return err
	}
	res, err := einvoice.Parse(data)
	if err != nil {
		return err
	}
	name := fs.Arg(0)
	if name == "-" {
		name = "stdin"
	}
	for _, is := range res.Issues {
		fmt.Fprintf(os.Stderr, "einv: %s: %s\n", name, is)
	}
	for _, p := range einvoice.Check(res.Invoice) {
		fmt.Fprintf(os.Stderr, "einv: %s: %v\n", name, p)
	}
	doc, err := json.MarshalIndent(res.Invoice, "", "  ")
	if err != nil {
		return err
	}
	doc = append(doc, '\n')
	switch *out {
	case "", "-":
		if _, err := os.Stdout.Write(doc); err != nil {
			return err
		}
	default:
		if err := os.WriteFile(*out, doc, 0o644); err != nil {
			return err
		}
	}
	fmt.Fprintf(os.Stderr, "einv: %s: %s %s with %d lines, %d parts the model has no room for\n",
		name, res.Syntax, docKind(res.Invoice), len(res.Invoice.Lines), len(res.Issues))
	return nil
}

// docKind names the document the model describes, so the summary line does not
// call a credit note an invoice.
func docKind(inv einvoice.Invoice) string {
	if strings.HasPrefix(inv.TypeCode, "381") || strings.HasPrefix(inv.TypeCode, "383") ||
		inv.TypeCode == "52" {
		return "credit note"
	}
	return "invoice"
}

func runDetect(args []string) error {
	if len(args) != 1 {
		return fmt.Errorf("detect: expected exactly one file")
	}
	data, err := readInput(args[0])
	if err != nil {
		return err
	}
	sx, err := einvoice.Detect(data)
	if err != nil {
		return err
	}
	fmt.Printf("%s: %s\n", filepath.Base(args[0]), sx)
	return nil
}

func runPhases(args []string) error {
	sx := einvoice.SyntaxCII
	if len(args) > 0 {
		var err error
		if sx, err = syntaxOf(args[0]); err != nil {
			return err
		}
	}
	ids, err := einvoice.Phases(sx)
	if err != nil {
		return err
	}
	fmt.Println(sx)
	for _, id := range ids {
		fmt.Println(" ", id)
	}
	return nil
}

// runRules lists the assertions this build carries, in the order a run applies
// them. It answers "what does version X enforce?" without a document to check,
// which is what a rules reference, a diff between releases or a compliance
// register needs. The JSON form is what the public site builds its reference
// from, so the page and the library cannot claim different rule sets.
func runRules(args []string) error {
	fs := flag.NewFlagSet("rules", flag.ContinueOnError)
	var (
		asJSON = fs.Bool("json", false, "emit the list as JSON")
		syn    = fs.String("syntax", "", "restrict to one syntax: cii or ubl (default: both)")
		phase  = fs.String("phase", "", "restrict to one rule phase id (see: einv phases)")
		cius   = fs.String("cius", "", "list a country or network rule set as well, e.g. xrechnung")
		prof   = fs.String("profile", "", "list a published bundle instead of our own artefacts, e.g. peppol")
	)
	if err := fs.Parse(args); err != nil {
		return err
	}
	opt, err := ruleSetOf("", *cius, *prof)
	if err != nil {
		return err
	}
	list := []einvoice.Rule{}
	for _, sx := range []einvoice.Syntax{einvoice.SyntaxCII, einvoice.SyntaxUBL} {
		if *syn != "" {
			want, err := syntaxOf(*syn)
			if err != nil {
				return err
			}
			if want != sx {
				continue
			}
		}
		more, err := einvoice.Rules(sx, opt)
		if err != nil {
			return err
		}
		for _, r := range more {
			if *phase != "" && !hasPhaseID(r.Phases, *phase) {
				continue
			}
			list = append(list, r)
		}
	}
	if *asJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(list)
	}
	for _, r := range list {
		fmt.Println(ruleLine(r))
	}
	return nil
}

// ruleLine renders one rule as one tab-separated line: syntax, id, severity,
// phase ids and the published text. Published text holds line breaks in four
// assertions, which would split a rule over two lines and break a grep for one
// id; the JSON form keeps that text exactly as it was published.
func ruleLine(r einvoice.Rule) string {
	return strings.Join([]string{
		string(r.Syntax),
		r.ID,
		r.Flag,
		strings.Join(r.Phases, ","),
		strings.Join(strings.Fields(r.Text), " "),
	}, "\t")
}

// hasPhaseID reports whether a rule belongs to a phase.
func hasPhaseID(phases []string, id string) bool {
	for _, p := range phases {
		if p == id {
			return true
		}
	}
	return false
}

// runCius lists the profile rule sets the library carries, or the phases of
// one of them. Names are what validate -cius takes.
func runCius(args []string) error {
	if len(args) == 0 {
		for _, n := range einvoice.CiusNames() {
			fmt.Printf("%s\t%s\n", n, einvoice.CiusVersion(einvoice.CIUS(n)))
		}
		return nil
	}
	c := einvoice.CIUS(strings.ToLower(strings.TrimSpace(args[0])))
	v := einvoice.CiusVersion(c)
	if v == "" {
		return fmt.Errorf("unknown profile %q (available: %s)", c, strings.Join(einvoice.CiusNames(), ", "))
	}
	fmt.Printf("%s (%s)\n", c, v)
	for _, sx := range []einvoice.Syntax{einvoice.SyntaxCII, einvoice.SyntaxUBL} {
		ids, err := einvoice.CiusPhases(c, sx)
		if err != nil {
			return err
		}
		fmt.Printf(" %s\n", sx)
		for _, id := range ids {
			fmt.Println("   ", id)
		}
	}
	return nil
}

// runProfile lists the published rule-set bundles the library carries, or the
// phases of one of them. Names are what validate -profile takes.
func runProfile(args []string) error {
	if len(args) == 0 {
		for _, n := range einvoice.ProfileNames() {
			fmt.Printf("%s\t%s\n", n, einvoice.ProfileVersion(einvoice.Profile(n)))
		}
		return nil
	}
	p := einvoice.Profile(strings.ToLower(strings.TrimSpace(args[0])))
	v := einvoice.ProfileVersion(p)
	if v == "" {
		return fmt.Errorf("unknown rule-set bundle %q (available: %s)", p, strings.Join(einvoice.ProfileNames(), ", "))
	}
	fmt.Printf("%s (%s)\n", p, v)
	for _, sx := range []einvoice.Syntax{einvoice.SyntaxCII, einvoice.SyntaxUBL} {
		ids, err := einvoice.ProfilePhases(p, sx)
		if err != nil {
			return err
		}
		fmt.Printf(" %s\n", sx)
		for _, id := range ids {
			fmt.Println("   ", id)
		}
	}
	return nil
}

// runEmbed adds the PDF/A-3 and Factur-X envelope to an ordinary PDF. The
// content of the PDF is left alone: a document whose fonts are not embedded
// stays that way, which is why the result is audited before it is announced.
func runEmbed(args []string) error {
	fs := flag.NewFlagSet("embed", flag.ExitOnError)
	var (
		prof  = fs.String("profile", "", "profile to declare (default: read from the XML)")
		conf  = fs.String("conformance", "B", "PDF/A-3 flavour: B, U or A (A requires -tagged)")
		out   = fs.String("o", "", "output PDF (default: stdout)")
		title = fs.String("title", "", "document title, shown in place of the file name")
		auth  = fs.String("author", "", "author")
		subj  = fs.String("subject", "", "subject")
		keys  = fs.String("keywords", "", "comma-separated keywords")
		tool  = fs.String("creator", "", "application that produced the content")
		lang  = fs.String("lang", "", "document language tag, e.g. en-GB")
		now   = fs.String("now", "", "timestamp to write, RFC 3339 (default: the current time)")
		tag   = fs.Bool("tagged", false, "the content stream is marked up: adds /MarkInfo and a structure tree")
		force = fs.Bool("force", false, "write the file even if the audit reports errors")
		extra stringList
	)
	fs.Var(&extra, "attach", "extra file to embed, repeatable")
	_ = fs.Parse(args)
	if fs.NArg() != 2 {
		return fmt.Errorf("embed: expected a PDF and an invoice XML")
	}

	pdfData, err := readInput(fs.Arg(0))
	if err != nil {
		return err
	}
	xmlData, err := readInput(fs.Arg(1))
	if err != nil {
		return err
	}

	opts := facturx.Options{
		Conformance: *conf,
		Tagged:      *tag,
		Metadata: facturx.Metadata{
			Title: *title, Author: *auth, Subject: *subj,
			Keywords: *keys, CreatorTool: *tool, Lang: *lang,
		},
	}
	if *prof != "" {
		p, err := facturx.ParseProfile(*prof)
		if err != nil {
			return err
		}
		opts.Profile = p
	}
	if *now != "" {
		t, err := time.Parse(time.RFC3339, *now)
		if err != nil {
			return fmt.Errorf("embed: -now wants an RFC 3339 time: %w", err)
		}
		opts.Now = t
	}
	for _, path := range extra {
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		opts.Attachments = append(opts.Attachments, facturx.Attachment{
			Filename: filepath.Base(path),
			Data:     data,
		})
	}

	wrapped, err := facturx.Embed(pdfData, xmlData, opts)
	if err != nil {
		return err
	}

	name := *out
	if name == "" || name == "-" {
		name = "stdout"
		if _, err := os.Stdout.Write(wrapped); err != nil {
			return err
		}
	} else if err := os.WriteFile(name, wrapped, 0o644); err != nil {
		return err
	}

	// Report on what was just written. The envelope this package adds is
	// always complete, so anything the audit finds belongs to the PDF that
	// came in: fonts, transparency, missing markup.
	_, rep, err := facturx.Audit(wrapped)
	if err != nil {
		return fmt.Errorf("embed: the file just written does not read back: %w", err)
	}
	if rep.OK() {
		fmt.Fprintf(os.Stderr, "einv: %s: hybrid document written\n", name)
		return nil
	}
	fmt.Fprintf(os.Stderr, "einv: %s: written, but a validator will reject it:\n%s", name, rep)
	if !*force {
		os.Exit(1)
	}
	return nil
}

// runExtract pulls the invoice XML out of a hybrid PDF.
func runExtract(args []string) error {
	fs := flag.NewFlagSet("extract", flag.ExitOnError)
	var (
		out  = fs.String("o", "", "output XML (default: stdout)")
		info = fs.Bool("info", false, "describe the envelope on stderr")
	)
	_ = fs.Parse(args)
	if fs.NArg() != 1 {
		return fmt.Errorf("extract: expected exactly one PDF (use - for stdin)")
	}
	data, err := readInput(fs.Arg(0))
	if err != nil {
		return err
	}
	doc, err := facturx.Inspect(data)
	if err != nil {
		return err
	}
	if len(doc.XML) == 0 {
		return fmt.Errorf("extract: %s carries no invoice XML", fs.Arg(0))
	}
	if *info {
		fmt.Fprintf(os.Stderr, "%s: envelope %s, XML says %s\n", fs.Arg(0), doc.Profile, doc.Declared)
		fmt.Fprintf(os.Stderr, "  payload %s (%d bytes), PDF %s, %d page(s)\n",
			doc.XMLFilename, len(doc.XML), doc.PDFVersion, doc.Pages)
		for _, fe := range doc.Files {
			flag := ""
			if !fe.DigestOK {
				flag = "  DIGEST MISMATCH"
			}
			fmt.Fprintf(os.Stderr, "  attached %-24s %-16s %d bytes%s\n",
				fe.Filename, fe.Relationship, fe.Size, flag)
		}
	}
	if *out == "" || *out == "-" {
		_, err := os.Stdout.Write(doc.XML)
		return err
	}
	return os.WriteFile(*out, doc.XML, 0o644)
}

// runCheck audits hybrid PDFs the way a recipient's validator would, without
// touching them.
func runCheck(args []string) error {
	fs := flag.NewFlagSet("check", flag.ExitOnError)
	var (
		asJSON = fs.Bool("json", false, "machine-readable JSON output")
		max    = fs.Int("max", 40, "maximum rule findings to report per document (-1 for all)")
		cius   = fs.String("cius", "", "check the invoice inside against a country or network rule set as well, e.g. xrechnung (see: einv cius)")
		prof   = fs.String("profile", "", "check the invoice inside against a published rule-set bundle instead of our own EN 16931 artefacts, e.g. peppol (see: einv profile)")
	)
	_ = fs.Parse(args)
	paths := fs.Args()
	if len(paths) == 0 {
		return fmt.Errorf("check: no input files (use - for stdin)")
	}

	opt, err := checkOptions(*cius, *prof)
	if err != nil {
		return err
	}
	// Settled once, before any file is opened, so a typo in a rule-set name is
	// not repeated for every document in the batch.
	if err := hybrid.CheckOptions(opt); err != nil {
		return err
	}

	all := make([]hybrid.Result, 0, len(paths))
	anyBad := false
	anyErr := false
	for _, path := range paths {
		data, err := readInput(path)
		if err != nil {
			all = append(all, hybrid.Result{CheckResult: facturx.CheckResult{Name: path, Error: err.Error()}})
			anyErr = true
			continue
		}
		e, err := hybrid.Check(data, opt)
		if err != nil {
			all = append(all, hybrid.Result{CheckResult: facturx.CheckResult{Name: path, Error: err.Error()}})
			anyErr = true
			continue
		}
		e.Name = path
		if !e.OK || (e.Rules != nil && !e.Rules.Valid) {
			anyBad = true
		}
		all = append(all, e)
	}

	if *asJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(all); err != nil {
			return err
		}
	} else {
		for _, e := range all {
			if e.Error != "" {
				fmt.Printf("%s: ERROR: %s\n", e.Name, e.Error)
				continue
			}
			if e.Envelope == "" || e.Payload == "" {
				fmt.Printf("%s: no invoice envelope, %d page(s)\n", e.Name, e.Pages)
			} else {
				fmt.Printf("%s: %s envelope, payload %s (XML says %s), %d page(s)\n",
					e.Name, e.Envelope, e.Payload, e.Declared, e.Pages)
			}
			if len(e.Findings) == 0 {
				fmt.Println("  no findings")
			}
			for _, f := range e.Findings {
				fmt.Printf("  %-7s %-22s %s\n", f.Severity, f.Rule, f.Message)
			}
			printRules(e.Rules, *max)
		}
	}
	switch {
	case anyBad:
		os.Exit(1)
	case anyErr:
		os.Exit(2)
	}
	return nil
}

// checkOptions turns the rule-set flags into engine options. The invoice inside
// a PDF is validated by the same engine that reads loose XML files, so the flags
// mean the same thing here as they do under validate.
func checkOptions(cius, prof string) (einvoice.Options, error) {
	opt := einvoice.Options{}
	if cius != "" {
		opt.CIUS = einvoice.CIUS(strings.ToLower(strings.TrimSpace(cius)))
	}
	if prof != "" {
		opt.Profile = einvoice.Profile(strings.ToLower(strings.TrimSpace(prof)))
	}
	return opt, nil
}

// printRules reports the business-rule verdict on the invoice inside a hybrid
// PDF. It is deliberately a separate block from the envelope findings: a
// recipient fixing a PDF/A defect should not have to wade through BR- findings
// to see whether the envelope is what let the file down.
func printRules(res *einvoice.Result, max int) {
	if res == nil {
		return
	}
	layers := string(res.Syntax)
	if res.Profile != "" {
		layers += " " + string(res.Profile)
	}
	if res.CIUS != "" {
		layers += " + " + string(res.CIUS)
	}
	if res.Valid {
		fmt.Printf("  invoice inside: valid %s document\n", layers)
		return
	}
	fmt.Printf("  invoice inside: INVALID %s invoice (%d fatal, %d warning, %d structural)\n",
		layers, res.Fatal, res.Warn, len(res.Schema))
	shown := res.Findings
	if max >= 0 && len(shown) > max {
		shown = shown[:max]
	}
	for _, f := range res.Schema {
		fmt.Printf("    %-12s %s at %s\n", "SCHEMA", f.Message, f.Location)
	}
	for _, f := range shown {
		loc := ""
		if f.Context != "" {
			loc = " at " + f.Context
		}
		fmt.Printf("    %-12s %s%s\n", f.ID, f.Message, loc)
	}
	if extra := len(res.Findings) - len(shown); extra > 0 {
		fmt.Printf("    ... and %d more (use -max -1 to list all)\n", extra)
	}
}

// stringList lets a flag be repeated, -attach a.csv -attach scan.pdf.
type stringList []string

func (s *stringList) String() string { return strings.Join(*s, ",") }

func (s *stringList) Set(v string) error {
	*s = append(*s, v)
	return nil
}

func syntaxOf(s string) (einvoice.Syntax, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "cii", "uncefact", "facturx", "zugferd", "xrechnung":
		return einvoice.SyntaxCII, nil
	case "ubl", "peppol", "oasis":
		return einvoice.SyntaxUBL, nil
	}
	return "", fmt.Errorf("unknown syntax %q (want cii or ubl)", s)
}

func readInput(path string) ([]byte, error) {
	if path == "-" {
		return io.ReadAll(os.Stdin)
	}
	return os.ReadFile(path)
}
