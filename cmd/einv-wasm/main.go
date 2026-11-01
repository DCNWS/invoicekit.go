//go:build js && wasm

// Command einv-wasm exposes the validator to JavaScript. Build with:
//
//	GOOS=js GOARCH=wasm go build -trimpath -ldflags="-s -w" -o dist/einv.wasm ./cmd/einv-wasm
//
// It installs a single global, `einvoice`, with:
//
//	validate(xml: string, opts?: {syntax?: string; phases?: string[]; cius?: string; profile?: string}): Result
//	detect(xml: string): string
//	phases(syntax?: string): string[]
//	cius(): {name, version, phases: {CII: string[], UBL: string[]}}[]
//	profiles(): {name, version, phases: {CII: string[], UBL: string[]}}[]
//	inspect(pdf: Uint8Array | ArrayBuffer, opts?: {syntax?: string; phases?: string[]; cius?: string; profile?: string}): CheckResult
//	extract(pdf: Uint8Array | ArrayBuffer): {name, xml, envelope}
//	parse(xml: string): {invoice, syntax, issues}
//	render(model: object | string, opts?): {xml, syntax, rules, problems}
//	embed(pdf: Uint8Array | ArrayBuffer, xml: string, envelope?, opts?):
//		{pdf: Uint8Array, check: CheckResult}
//
// opts.cius names a country rule set, e.g. "xrechnung", which is checked on top
// of EN 16931; opts.profile names a network's own rule-set bundle, e.g.
// "peppol", which replaces this build's EN 16931 artefacts with the bundle's.
// cius() and profiles() list what a build carries.
//
// inspect and extract take a hybrid PDF — an invoice PDF carrying its own XML,
// which is what Factur-X and ZUGFeRD are — so a page can audit and read one
// without uploading it anywhere. inspect answers two questions at once and keeps
// the answers apart: ok and findings are the envelope, and the rules block is
// the business-rule verdict on the invoice inside, against the rule set opts
// asks for. A document with no XML inside, or one whose XML cannot be read, gets
// no rules block; in the second case a finding says why.
//
// embed goes the other way: it wraps an ordinary PDF and an invoice XML in the
// Factur-X / Order-X envelope, then audits the bytes it just wrote, so a page can
// make a hybrid document and see the verdict on it in the same call. The third
// argument configures the envelope — profile, conformance, metadata, tagged — and
// the fourth is the rule set to run over the invoice inside, the same object
// validate and inspect take. They are separate arguments because "profile" means
// two different things: the Factur-X profile the PDF declares, and a network's
// rule-set bundle. One object would let a choice in one box silently change the
// other.
//
// embed fixes the envelope, not the document. A PDF whose fonts are not embedded
// still has unembedded fonts afterwards — those are properties of the content,
// not of the metadata — and the audit it returns says so instead of announcing a
// clean file.
//
// parse says what a document says rather than whether it obeys the rules: it
// reads an invoice or credit note in either syntax into the same model `render`
// takes, and lists what the document said that the model has no room for. A
// document that breaks business rules still parses, with the amounts it states,
// wrong ones included — that is the point of running parse and validate over the
// same bytes.
//
// render goes the other way, and validates its own output the way `einv gen`
// does: the model is stamped with the identifiers the named rule set expects,
// rendered in opts.syntax, and checked, so a model that cannot satisfy Peppol or
// XRechnung fails here instead of in a customer's inbox. parse and render
// together convert a document from one syntax to the other without a server.
//
// The result shapes match the JSON the hosted API and `einv check -json`
// return, so browser, Go and server all speak the same format.
package main

import (
	"encoding/json"
	"fmt"
	"strings"
	"syscall/js"
	"time"

	"github.com/dcnws/invoicekit.go"
	"github.com/dcnws/invoicekit.go/facturx"
	"github.com/dcnws/invoicekit.go/hybrid"
)

func main() {
	obj := js.Global().Get("Object").New()
	obj.Set("validate", js.FuncOf(validate))
	obj.Set("detect", js.FuncOf(detect))
	obj.Set("phases", js.FuncOf(phases))
	obj.Set("cius", js.FuncOf(cius))
	obj.Set("profiles", js.FuncOf(profiles))
	obj.Set("inspect", js.FuncOf(inspect))
	obj.Set("extract", js.FuncOf(extract))
	obj.Set("parse", js.FuncOf(parseDoc))
	obj.Set("render", js.FuncOf(render))
	obj.Set("embed", js.FuncOf(embed))
	obj.Set("version", einvoice.Version)
	js.Global().Set("einvoice", obj)
	// The Go runtime reaches main() asynchronously after go.run() returns, so the
	// host page must be told when the API is actually there.
	if cb := js.Global().Get("onEinvoiceLoaded"); cb.Type() == js.TypeFunction {
		cb.Invoke()
	}
	select {}
}

func options(v js.Value) einvoice.Options {
	opt := einvoice.Options{}
	if v.Equal(js.Value{}) || v.IsUndefined() || v.IsNull() || v.Type() != js.TypeObject {
		return opt
	}
	if s := v.Get("syntax"); s.Type() == js.TypeString {
		switch s.String() {
		case "CII", "cii":
			opt.Syntax = einvoice.SyntaxCII
		case "UBL", "ubl":
			opt.Syntax = einvoice.SyntaxUBL
		}
	}
	if p := v.Get("phases"); p.Type() == js.TypeObject && p.Get("length").Type() == js.TypeNumber {
		n := p.Get("length").Int()
		for i := 0; i < n; i++ {
			if e := p.Index(i); e.Type() == js.TypeString {
				opt.Phases = append(opt.Phases, e.String())
			}
		}
	}
	if c := v.Get("cius"); c.Type() == js.TypeString {
		opt.CIUS = einvoice.CIUS(strings.ToLower(strings.TrimSpace(c.String())))
	}
	if p := v.Get("profile"); p.Type() == js.TypeString {
		opt.Profile = einvoice.Profile(strings.ToLower(strings.TrimSpace(p.String())))
	}
	return opt
}

func validate(_ js.Value, args []js.Value) any {
	if len(args) == 0 {
		return errValue("validate: missing the XML string argument")
	}
	var opts js.Value
	if len(args) > 1 {
		opts = args[1]
	}
	return guard(func() any {
		res, err := einvoice.Validate([]byte(args[0].String()), options(opts))
		if err != nil {
			return errValue(err.Error())
		}
		return jsonValue(res)
	})
}

func detect(_ js.Value, args []js.Value) any {
	if len(args) == 0 {
		return errValue("detect: missing the XML string argument")
	}
	return guard(func() any {
		sx, err := einvoice.Detect([]byte(args[0].String()))
		if err != nil {
			return errValue(err.Error())
		}
		return string(sx)
	})
}

func phases(_ js.Value, args []js.Value) any {
	sx := einvoice.SyntaxCII
	if len(args) > 0 {
		switch args[0].String() {
		case "UBL", "ubl":
			sx = einvoice.SyntaxUBL
		}
	}
	return guard(func() any {
		ids, err := einvoice.Phases(sx)
		if err != nil {
			return errValue(err.Error())
		}
		return jsonValue(ids)
	})
}

// ruleSet is one rule set a build carries, with the phases of each syntax, so a
// page can offer it as a checkbox instead of hard-coding names.
type ruleSet struct {
	Name    string              `json:"name"`
	Version string              `json:"version"`
	Phases  map[string][]string `json:"phases"`
}

// ruleSets lists named rule sets of one kind, each with its phases. listing and
// phasesOf are what differ between a CIUS and a profile bundle.
func ruleSets(names []string, version func(string) string, phases func(name string, sx einvoice.Syntax) ([]string, error)) any {
	out := make([]ruleSet, 0, len(names))
	for _, name := range names {
		p := ruleSet{Name: name, Version: version(name), Phases: map[string][]string{}}
		for _, sx := range []einvoice.Syntax{einvoice.SyntaxCII, einvoice.SyntaxUBL} {
			ids, err := phases(name, sx)
			if err != nil {
				return errValue(err.Error())
			}
			p.Phases[string(sx)] = ids
		}
		out = append(out, p)
	}
	return jsonValue(out)
}

// profiles describes the published rule-set bundles this build carries, e.g.
// Peppol BIS Billing 3.0, which validate replaces the built-in EN 16931
// artefacts with.
func profiles(_ js.Value, args []js.Value) any {
	return guard(func() any {
		return ruleSets(einvoice.ProfileNames(),
			func(n string) string { return einvoice.ProfileVersion(einvoice.Profile(n)) },
			func(n string, sx einvoice.Syntax) ([]string, error) {
				return einvoice.ProfilePhases(einvoice.Profile(n), sx)
			})
	})
}

// cius describes the country rule sets this build carries, with the phases of
// each, so a page can offer them as a checkbox instead of hard-coding names.
func cius(_ js.Value, args []js.Value) any {
	return guard(func() any {
		return ruleSets(einvoice.CiusNames(),
			func(n string) string { return einvoice.CiusVersion(einvoice.CIUS(n)) },
			func(n string, sx einvoice.Syntax) ([]string, error) { return einvoice.CiusPhases(einvoice.CIUS(n), sx) })
	})
}

// maxPDFBytes bounds what the browser build will take apart. A page that hands
// over a two-gigabyte video would otherwise spend the tab's whole memory
// parsing a file no invoice could possibly be.
const maxPDFBytes = 64 << 20

// pdfBytes copies a Uint8Array, Buffer or ArrayBuffer from JavaScript into Go
// memory. The copy is the point: nothing in the Go heap aliases the page's
// buffer, so the bytes stay readable by the caller afterwards.
func pdfBytes(v js.Value) ([]byte, error) {
	if v.Type() != js.TypeObject {
		return nil, fmt.Errorf("expected the PDF bytes as a Uint8Array or ArrayBuffer")
	}
	view := v
	switch {
	case v.InstanceOf(js.Global().Get("Uint8Array")):
	case v.InstanceOf(js.Global().Get("ArrayBuffer")):
		view = js.Global().Get("Uint8Array").New(v)
	default:
		return nil, fmt.Errorf("expected the PDF bytes as a Uint8Array or ArrayBuffer")
	}
	n := view.Get("byteLength").Int()
	if n > maxPDFBytes {
		return nil, fmt.Errorf("the file is %d bytes, past the %d MB this build handles", n, maxPDFBytes>>20)
	}
	b := make([]byte, n)
	js.CopyBytesToGo(b, view)
	return b, nil
}

// inspect audits a hybrid PDF exactly as `einv check` does and returns the same
// JSON, so a browser verdict and a CI verdict can be compared field by field.
// The invoice inside is validated against the rule set the second argument asks
// for, which is the same object validate takes.
func inspect(_ js.Value, args []js.Value) any {
	if len(args) == 0 {
		return errValue("inspect: missing the PDF bytes argument")
	}
	return guard(func() any {
		data, err := pdfBytes(args[0])
		if err != nil {
			return errValue("inspect: " + err.Error())
		}
		opt := einvoice.Options{}
		if len(args) > 1 {
			opt = options(args[1])
		}
		res, err := hybrid.Check(data, opt)
		if err != nil {
			return errValue("inspect: " + err.Error())
		}
		return jsonValue(res)
	})
}

// extract returns the invoice XML inside a hybrid PDF, which is the step a
// recipient needs before they can validate or import the document.
func extract(_ js.Value, args []js.Value) any {
	if len(args) == 0 {
		return errValue("extract: missing the PDF bytes argument")
	}
	return guard(func() any {
		data, err := pdfBytes(args[0])
		if err != nil {
			return errValue("extract: " + err.Error())
		}
		doc, err := facturx.Inspect(data)
		if err != nil {
			return errValue("extract: " + err.Error())
		}
		if len(doc.XML) == 0 {
			return errValue("extract: this PDF carries no invoice XML, so there is nothing to read out")
		}
		return jsonValue(map[string]any{
			"name":     doc.XMLFilename,
			"envelope": doc.Profile.String(),
			"xml":      string(doc.XML),
		})
	})
}

// parseDoc reads a document into the invoice model. It is named for the library
// call it wraps, so the handler and einvoice.Parse read as one operation.
func parseDoc(_ js.Value, args []js.Value) any {
	if len(args) == 0 {
		return errValue("parse: missing the XML string argument")
	}
	return guard(func() any {
		res, err := einvoice.Parse([]byte(args[0].String()))
		if err != nil {
			return errValue(err.Error())
		}
		return jsonValue(res)
	})
}

// render turns a model into a document and checks the document it made, which is
// the same bargain the CLI makes: the caller gets a verdict on the output rather
// than only on the input. The model may arrive as an object or as a JSON string,
// because a page usually has one and a test usually has the other.
func render(_ js.Value, args []js.Value) any {
	if len(args) == 0 {
		return errValue("render: missing the invoice model argument")
	}
	var opts js.Value
	if len(args) > 1 {
		opts = args[1]
	}
	return guard(func() any {
		src := args[0]
		if src.Type() != js.TypeString {
			src = js.Global().Get("JSON").Call("stringify", src)
		}
		inv, err := einvoice.DecodeInvoiceModel(strings.NewReader(src.String()))
		if err != nil {
			return errValue(err.Error())
		}
		opt := options(opts)
		if opt.Syntax == "" {
			opt.Syntax = einvoice.SyntaxUBL
		}
		// The document has to name the rule set it aims at before that rule set
		// will read a line of it, exactly as in the CLI.
		if err := einvoice.ApplySpec(&inv, opt); err != nil {
			return errValue(err.Error())
		}
		doc, err := einvoice.Generate(opt.Syntax, inv)
		if err != nil {
			return errValue(err.Error())
		}
		res, err := einvoice.Validate(doc, opt)
		if err != nil {
			return errValue("render: the generated document does not parse: " + err.Error())
		}
		// Advisory: the model's own sums disagree with its lines. Rendering does
		// not depend on the verdict, so it travels as a list, not as a failure.
		problems := make([]any, 0)
		for _, pb := range einvoice.Check(inv) {
			problems = append(problems, pb.Error())
		}
		out := js.Global().Get("Object").New()
		out.Set("xml", string(doc))
		out.Set("syntax", string(opt.Syntax))
		out.Set("problems", js.ValueOf(problems))
		out.Set("rules", jsonValue(res))
		return out
	})
}

// embedOptions decodes embed's third argument: the envelope to write, not the
// rule set to run. Absent and empty fields mean the defaults Embed already
// documents — profile read from the XML, PDF/A-3B, timestamps as of now.
func embedOptions(v js.Value) (facturx.Options, error) {
	opt := facturx.Options{}
	if v.Equal(js.Value{}) || v.IsUndefined() || v.IsNull() || v.Type() != js.TypeObject {
		return opt, nil
	}
	if s := v.Get("profile"); s.Type() == js.TypeString {
		p, err := facturx.ParseProfile(s.String())
		if err != nil {
			return opt, err
		}
		opt.Profile = p
	}
	if s := v.Get("conformance"); s.Type() == js.TypeString {
		opt.Conformance = s.String()
	}
	md := &opt.Metadata
	for _, f := range []struct {
		key string
		dst *string
	}{{"title", &md.Title}, {"author", &md.Author}, {"subject", &md.Subject},
		{"keywords", &md.Keywords}, {"creator", &md.CreatorTool}, {"lang", &md.Lang}} {
		if s := v.Get(f.key); s.Type() == js.TypeString {
			*f.dst = s.String()
		}
	}
	if t := v.Get("tagged"); t.Type() == js.TypeBoolean {
		opt.Tagged = t.Bool()
	}
	// now pins the timestamps written into XMP, /Info and the attachment
	// parameters, so the same inputs produce the same bytes — which is what makes
	// the output testable, and what an archive needs to show a document has not
	// been rewritten since it was issued.
	if s := v.Get("now"); s.Type() == js.TypeString {
		t, err := time.Parse(time.RFC3339, s.String())
		if err != nil {
			return opt, fmt.Errorf("now wants an RFC 3339 time: %w", err)
		}
		opt.Now = t
	}
	return opt, nil
}

// embed makes a hybrid PDF: the envelope Factur-X and ZUGFeRD require around an
// invoice XML, then the same audit inspect would give the file it just wrote,
// under the rule set the fourth argument asks for. Returning the verdict with the
// bytes is the point — a generator that announces "done" without checking sells a
// file a recipient may reject.
func embed(_ js.Value, args []js.Value) any {
	if len(args) < 2 {
		return errValue("embed: expected the PDF bytes and the invoice XML")
	}
	var envelope, rules js.Value
	if len(args) > 2 {
		envelope = args[2]
	}
	if len(args) > 3 {
		rules = args[3]
	}
	return guard(func() any {
		data, err := pdfBytes(args[0])
		if err != nil {
			return errValue("embed: " + err.Error())
		}
		if args[1].IsUndefined() || args[1].IsNull() {
			return errValue("embed: expected the PDF bytes and the invoice XML")
		}
		if args[1].Type() != js.TypeString {
			return errValue("embed: the invoice XML must be a string")
		}
		opts, err := embedOptions(envelope)
		if err != nil {
			return errValue("embed: " + err.Error())
		}
		wrapped, err := facturx.Embed(data, []byte(args[1].String()), opts)
		if err != nil {
			return errValue("embed: " + err.Error())
		}
		check, err := hybrid.Check(wrapped, options(rules))
		if err != nil {
			return errValue("embed: the file just written does not read back: " + err.Error())
		}
		out := js.Global().Get("Object").New()
		out.Set("pdf", jsBytes(wrapped))
		out.Set("check", jsonValue(check))
		return out
	})
}

// jsBytes copies Go bytes into a JavaScript Uint8Array. The copy matters in this
// direction too: the page keeps a buffer it can download and re-inspect, and
// nothing in it aliases the Go heap, so the engine is free to move on.
func jsBytes(b []byte) js.Value {
	out := js.Global().Get("Uint8Array").New(len(b))
	js.CopyBytesToJS(out, b)
	return out
}

// guard turns a panic inside an exported callback into an error result. A panic
// in a js.FuncOf handler would otherwise terminate the whole Go instance and
// leave the page with a dead validator.
func guard(fn func() any) (out any) {
	defer func() {
		if r := recover(); r != nil {
			out = errValue(fmt.Sprintf("einvoice: internal error: %v", r))
		}
	}()
	return fn()
}

func jsonValue(v any) js.Value {
	data, err := json.Marshal(v)
	if err != nil {
		return errValue("encode: " + err.Error())
	}
	var out any
	if err := json.Unmarshal(data, &out); err != nil {
		return errValue("decode: " + err.Error())
	}
	return js.ValueOf(out)
}

func errValue(msg string) js.Value {
	return js.ValueOf(map[string]any{"error": msg})
}
