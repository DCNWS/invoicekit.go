package facturx

import (
	"fmt"
	"sort"
	"strings"

	"github.com/dcnws/invoicekit.go/internal/pdf"
)

// Severity ranks a finding. Anything at SeverityError would make a validator
// reject the file; warnings are things a validator lets pass but a recipient
// may still complain about.
type Severity string

const (
	SeverityError   Severity = "error"
	SeverityWarning Severity = "warning"
	SeverityInfo    Severity = "info"
)

// Finding is one observation about a hybrid document.
type Finding struct {
	// Rule is the stable identifier of the check, e.g. "fx.digest", so
	// callers can react to a specific defect without parsing the message.
	Rule string `json:"rule"`

	// Severity is how much the finding matters: error means a validator
	// rejects the file, warning means a recipient may complain, info is a
	// note about how the document behaves in a reader.
	Severity Severity `json:"severity"`

	Message string `json:"message"`
}

// Report is the outcome of an audit.
type Report struct {
	Findings []Finding
}

// OK reports whether nothing rose to an error.
func (r *Report) OK() bool {
	for _, f := range r.Findings {
		if f.Severity == SeverityError {
			return false
		}
	}
	return true
}

// Counts returns how many findings there are at each severity.
func (r *Report) Counts() map[Severity]int {
	m := map[Severity]int{}
	for _, f := range r.Findings {
		m[f.Severity]++
	}
	return m
}

// String renders the findings one per line, most severe first.
func (r *Report) String() string {
	order := map[Severity]int{SeverityError: 0, SeverityWarning: 1, SeverityInfo: 2}
	fs := append([]Finding(nil), r.Findings...)
	sort.SliceStable(fs, func(i, j int) bool { return order[fs[i].Severity] < order[fs[j].Severity] })
	var b strings.Builder
	for _, f := range fs {
		fmt.Fprintf(&b, "%-7s %-22s %s\n", f.Severity, f.Rule, f.Message)
	}
	return b.String()
}

func (r *Report) add(sev Severity, rule, format string, args ...any) {
	r.Findings = append(r.Findings, Finding{Severity: sev, Rule: rule, Message: fmt.Sprintf(format, args...)})
}

// CheckResult is one document's audit of its envelope, in the form every
// surface sends: the CLI's -json output, the hosted API and the browser build
// all encode this, so a rule id means the same thing whoever produced it.
type CheckResult struct {
	// Name is the caller's label for the document - a filename, a request id.
	// Result leaves it empty because it has no idea how the bytes arrived.
	Name string `json:"name,omitempty"`

	// Envelope is the profile the document claims, such as "Factur-X EN 16931".
	Envelope string `json:"envelope,omitempty"`

	// Payload is the filename of the invoice XML inside the PDF, when there is one.
	Payload string `json:"payload,omitempty"`

	// Declared is what the XML itself says the document is, which is the half
	// that can disagree with Envelope.
	Declared string `json:"declared,omitempty"`

	Pages int `json:"pages,omitempty"`

	// OK reports whether a conformance validator would accept the envelope. It
	// says nothing about the invoice inside: business rules are a separate
	// verdict, reported by the rule engine that sits above this package.
	OK bool `json:"ok"`

	Findings []Finding `json:"findings,omitempty"`

	// Error holds why the document could not be audited at all, which is a
	// different thing from an audit that found faults: a caller batching files
	// still needs to tell "broken" from "unreadable".
	Error string `json:"error,omitempty"`
}

// Result folds an inspected document and its report into wire form. It lives
// here rather than as a Check helper so that this package stays below the rule
// engine: pairing the envelope verdict with a business-rule verdict is the job
// of a caller that is allowed to know about both.
func Result(doc *Document, rep *Report) CheckResult {
	return CheckResult{
		Envelope: doc.Profile.String(),
		Payload:  doc.XMLFilename,
		Declared: doc.Declared.String(),
		Pages:    doc.Pages,
		OK:       rep.OK(),
		Findings: rep.Findings,
	}
}

// Audit inspects a hybrid PDF and reports why it would or would not be accepted
// as a Factur-X document. It covers the envelope thoroughly — PDF/A-3 metadata,
// output intent, the attachment declaration, the relationship between the XML
// and what the PDF claims about it — and the content only where a defect there
// guarantees rejection: fonts that are not embedded.
//
// Validating the XML payload against EN 16931 is a separate step, done by
// einvoice.Validate; keeping the two apart means a PDF/A problem never hides a
// business-rule problem, and vice versa.
func Audit(data []byte) (*Document, *Report, error) {
	doc, err := Inspect(data)
	if err != nil {
		return nil, nil, err
	}
	f, err := pdf.Parse(data)
	if err != nil {
		return nil, nil, err
	}
	root, err := f.Root()
	if err != nil {
		return nil, nil, err
	}
	a := &audit{f: f, root: root, doc: doc, rep: &Report{},
		// The attachment declaration is read up front because the PDF/A part
		// and the presence of attachments have to be judged together: part 1
		// forbids attachments outright, so a part-1 hybrid is a different
		// defect from a part-1 document that simply carries its XML beside it.
		af:    afFilespecs(f, root),
		names: embeddedFileNames(f, root),
	}
	for _, check := range checks {
		check(a)
	}
	return doc, a.rep, nil
}

// audit is one document under inspection: the parsed PDF, the inventory
// Inspect built, and the report each check writes to.
type audit struct {
	f     *pdf.File
	root  *pdf.Dict
	doc   *Document
	rep   *Report
	af    []pdf.Object
	names []nameEntry
}

// checks are the rules Audit applies, in reporting order. A validator's
// rejection letter reads in this order too, which is why the metadata comes
// before the payload and the payload before the niceties.
var checks = []func(*audit){
	(*audit).metadata,
	(*audit).colour,
	(*audit).payload,
	(*audit).attachmentDeclaration,
	(*audit).readerHints,
	(*audit).structure,
	(*audit).infoDict,
	(*audit).fonts,
}

// metadata judges the XMP packet and the header against PDF/A-3.
func (a *audit) metadata() {
	if !a.doc.XMP.HasPacket {
		a.rep.add(SeverityError, "pdfa.xmp", "no XMP metadata stream: PDF/A requires /Metadata in the catalogue with /Subtype /XML")
		return
	}
	auditPart(a.rep, a.doc.XMP.Part, len(a.names), a.doc.XMP.Level != "" || a.doc.Profile.Flavor != "")
	if a.doc.XMP.Conf == "" {
		a.rep.add(SeverityWarning, "pdfa.conformance", "XMP declares no pdfaid:conformance level (B, U or A)")
	}
	if a.doc.XMP.Level != "" && !a.doc.XMP.HasSchema {
		a.rep.add(SeverityError, "pdfa.schema", "XMP uses hybrid-document properties without declaring them in a pdfaExtension schema block; validators reject undeclared properties")
	}
}

// colour and version: the two envelope properties that are easy to forget and
// expensive to discover later.
func (a *audit) colour() {
	if v := a.doc.PDFVersion; v != "" && v < "1.7" {
		a.rep.add(SeverityWarning, "pdf.version", "header says PDF %s; PDF/A-3 is defined against PDF 1.7", v)
	}
	if !hasOutputIntent(a.f, a.root) {
		a.rep.add(SeverityError, "pdfa.outputintent", "no OutputIntent with an ICC profile: PDF/A requires one so colour survives 50 years")
	}
}

// payload compares the attached XML with what the PDF says about it.
func (a *audit) payload() {
	if len(a.doc.XML) == 0 {
		a.rep.add(SeverityError, "fx.payload", "the PDF carries no invoice XML: it is a PDF, not a hybrid document")
		return
	}
	a.payloadEntry()
	a.payloadProfile()
	if n := len(a.doc.XMLAttachments); n > 1 {
		a.rep.add(SeverityWarning, "fx.multipayload", "%d XML attachments are present (%s): a hybrid invoice carries its XML once, and a reader takes whichever it finds first — remove the extras", n, strings.Join(a.doc.XMLAttachments, ", "))
	}
}

// payloadEntry checks the file entry of the attachment that holds the invoice.
func (a *audit) payloadEntry() {
	if want := a.doc.Profile.Filename(); want != "" && !strings.EqualFold(a.doc.XMLFilename, want) {
		a.rep.add(SeverityWarning, "fx.filename", "payload is attached as %q, but %s expects the name %q; strict readers may not find it", a.doc.XMLFilename, a.doc.Profile, want)
	}
	e := a.doc.payloadEntry()
	if e == nil {
		return
	}
	if e.Relationship != "Data" {
		a.rep.add(SeverityWarning, "fx.afrelationship", "payload has AFRelationship %q; the invoice XML should be /Data", e.Relationship)
	}
	switch {
	case len(e.RecordedMD5) == 0:
		a.rep.add(SeverityError, "pdfa.fileparams", "embedded file %q has no /CheckSum: PDF/A-3 requires the digest and size of every embedded file", a.doc.XMLFilename)
	case !e.DigestOK:
		a.rep.add(SeverityError, "fx.digest", "embedded file %q does not match its recorded MD5: the payload was altered after the digest was computed", a.doc.XMLFilename)
	}
	if e.Size > 0 && e.Size != len(a.doc.XML) {
		a.rep.add(SeverityWarning, "pdfa.filesize", "embedded file %q declares %d bytes and holds %d", a.doc.XMLFilename, e.Size, len(a.doc.XML))
	}
}

// payloadProfile compares the profile declared in the PDF with the one the XML
// declares for itself.
func (a *audit) payloadProfile() {
	declared, profile := a.doc.Declared, a.doc.Profile
	if declared.Flavor != "" && profile.Flavor != "" && declared.Flavor != profile.Flavor {
		a.rep.add(SeverityError, "fx.flavour", "the PDF declares %s but the XML says %s", profile.Flavor, declared.Flavor)
	}
	switch {
	case declared.Flavor != "" && declared.Level == "":
		a.rep.add(SeverityWarning, "fx.context", "the XML declares no guideline identifier (BT-24), so its profile cannot be established from the payload")
	case profile.Level != "" && declared.Level != "" && profile.Level != declared.Level:
		a.rep.add(SeverityError, "fx.level", "the PDF declares level %s but the XML declares %s", profile.Level, declared.Level)
	}
}

// attachmentDeclaration checks /AF against the embedded files actually present.
func (a *audit) attachmentDeclaration() {
	if len(a.names) > 0 && len(a.af) == 0 {
		a.rep.add(SeverityError, "pdfa.af", "embedded files are present but the catalogue has no /AF array: PDF/A-3 requires every embedded file to be declared there")
		return
	}
	if len(a.names) != len(a.af) && len(a.af) > 0 {
		a.rep.add(SeverityWarning, "pdfa.af", "/AF lists %d file specifications while /EmbeddedFiles lists %d; they should agree", len(a.af), len(a.names))
	}
}

// readerHints are the catalogue entries that decide how a viewer presents the
// document rather than whether a validator accepts it.
func (a *audit) readerHints() {
	if !a.root.Has("Lang") {
		a.rep.add(SeverityWarning, "pdf.lang", "the catalogue has no /Lang, so text cannot be rendered or read aloud correctly")
	}
	if vp := a.root.DictValue("ViewerPreferences", a.f); vp == nil || vp.Get("DisplayDocTitle") != pdf.Bool(true) {
		a.rep.add(SeverityWarning, "pdf.doctitle", "/ViewerPreferences /DisplayDocTitle is not true, so readers show the file name instead of the invoice title")
	}
	if a.root.NameValue("PageMode") != "UseAttachments" && len(a.doc.XML) > 0 {
		a.rep.add(SeverityInfo, "fx.pagemode", "/PageMode is not /UseAttachments: readers will open on the page rather than the attachment panel")
	}
}

// structure covers the tagging tree, which only level A insists on.
func (a *audit) structure() {
	tagged := a.root.DictValue("StructTreeRoot", a.f) != nil
	if a.doc.XMP.Conf != "A" {
		if !tagged {
			a.rep.add(SeverityInfo, "pdfa.tagged", "document is not tagged: fine for PDF/A-3b and 3u, but it can never claim level A")
		}
		return
	}
	if mi := a.root.DictValue("MarkInfo", a.f); mi == nil || mi.Get("Marked") != pdf.Bool(true) {
		a.rep.add(SeverityError, "pdfa.tagged", "XMP claims conformance level A but the document is not marked as tagged (/MarkInfo /Marked true)")
	}
	if !tagged {
		a.rep.add(SeverityError, "pdfa.structtree", "XMP claims level A but there is no /StructTreeRoot")
	}
}

// infoDict checks that the information dictionary agrees with the XMP packet,
// which PDF/A requires even though /Info is legacy.
func (a *audit) infoDict() {
	info := a.f.Trailer.DictValue("Info", a.f)
	if info == nil || !a.doc.XMP.HasPacket {
		return
	}
	for _, pair := range []struct{ key, got, want string }{
		{"Title", decodeText(info.StringValue("Title")), a.doc.XMP.Title},
		{"Author", decodeText(info.StringValue("Author")), a.doc.XMP.Creator},
		{"Subject", decodeText(info.StringValue("Subject")), a.doc.XMP.Description},
		{"Producer", decodeText(info.StringValue("Producer")), a.doc.XMP.Producer},
	} {
		if pair.got != "" && pair.want != "" && pair.got != pair.want {
			a.rep.add(SeverityWarning, "pdfa.info", "/Info /%s (%q) disagrees with XMP (%q); PDF/A requires the two to match", pair.key, pair.got, pair.want)
		}
	}
}

// fonts are the one content defect checked here. The catalogue is not the page
// tree: asking it for /Kids finds nothing, which would let an unembedded font
// through unnoticed.
func (a *audit) fonts() {
	tree := a.root.DictValue("Pages", a.f)
	if tree == nil {
		return
	}
	for _, name := range unembeddedFonts(a.f, tree) {
		a.rep.add(SeverityError, "pdfa.font", "font %q is not embedded: PDF/A forbids the standard 14 fonts unless a copy travels with the file", name)
	}
}

// auditPart judges the PDF/A part a hybrid declares. Factur-X and Order-X are
// defined against PDF/A-3, and the reason is concrete rather than historical:
// PDF/A-1 forbids embedded files altogether (ISO 19005-1 6.1.11 — no
// /EmbeddedFiles in the name dictionary, no /EF in a file specification), and
// PDF/A-2 admits them only where the embedded file is itself conformant
// (ISO 19005-2 6.8.5). So a document declaring part 1 is either a hybrid that
// will be rejected by the validator it invites, or an older ZUGFeRD 1.0 style
// arrangement in which the XML travels beside the PDF instead of inside it.
// Those two need saying differently.
func auditPart(rep *Report, part, attachments int, hybrid bool) {
	switch part {
	case 3:
		return
	case 0:
		rep.add(SeverityError, "pdfa.part", "the XMP packet declares no pdfaid:part, so the file never says which PDF/A it claims: an archive cannot validate a promise nobody made")
	case 1:
		if attachments > 0 {
			rep.add(SeverityError, "pdfa.attachments", "the file declares PDF/A-1 and carries %d embedded file(s): ISO 19005-1 6.1.11 forbids /EmbeddedFiles in the name dictionary and /EF in a file specification, so a validator rejects it — declare PDF/A-3 or send the XML beside the PDF", attachments)
			return
		}
		if hybrid {
			rep.add(SeverityError, "pdfa.part", "the XMP describes a hybrid invoice but declares PDF/A-1, which cannot carry an attachment: the payload has to travel beside this PDF as a separate file")
			rep.add(SeverityInfo, "fx.companion", "the PDF holds no XML: in the PDF/A-1 arrangement the invoice is a companion file of the same name with a .xml extension, and nothing about the payload can be checked without it")
			return
		}
		rep.add(SeverityError, "pdfa.part", "XMP declares pdfaid:part 1: a hybrid invoice needs PDF/A-3, because PDF/A-1 forbids embedded files")
	case 2:
		rep.add(SeverityError, "pdfa.part", "XMP declares pdfaid:part 2: PDF/A-2 admits an attachment only when the embedded file is itself conformant (ISO 19005-2 6.8.5), which no Factur-X reader checks; hybrid invoices are specified against PDF/A-3")
	default:
		rep.add(SeverityError, "pdfa.part", "XMP declares pdfaid:part %d, which is not a PDF/A part; a hybrid invoice is specified against PDF/A-3", part)
	}
}

// hasOutputIntent reports whether the catalogue carries a PDF/A output intent
// with a profile attached.
func hasOutputIntent(f *pdf.File, root *pdf.Dict) bool {
	for _, v := range root.ArrayValue("OutputIntents") {
		var d *pdf.Dict
		switch o := v.(type) {
		case pdf.Ref:
			d, _ = f.Object(o.Num).(*pdf.Dict)
		case *pdf.Dict:
			d = o
		}
		if d == nil {
			continue
		}
		if d.StreamValue("DestOutputProfile", f) != nil {
			return true
		}
	}
	return false
}

// afFilespecs returns the entries of the catalogue's /AF array.
func afFilespecs(f *pdf.File, root *pdf.Dict) []pdf.Object {
	switch v := root.Get("AF").(type) {
	case pdf.Array:
		return v
	case nil:
		return nil
	}
	return pdf.Array{root.Get("AF")}
}

// unembeddedFonts names the fonts a page references without carrying a copy of.
// A base-14 font such as Helvetica is the usual offender: it renders fine on a
// desktop and fails validation, because PDF/A cannot assume a renderer owns it.
// unembeddedFonts names the fonts a page draws with that do not travel inside
// the file. pageTree is the root of the page tree, not the catalogue.
func unembeddedFonts(f *pdf.File, pageTree *pdf.Dict) []string {
	seen := map[string]bool{}
	for _, page := range pages(f, pageTree, 0) {
		res := page.DictValue("Resources", f)
		if res == nil {
			res = inheritedResources(f, page, 0)
		}
		fonts := res.DictValue("Font", f)
		if fonts == nil {
			continue
		}
		for _, key := range fonts.Keys() {
			fd, base := fontDescriptor(f, fonts.Get(key), 0)
			if embedded(fd) {
				continue
			}
			seen[fontLabel(key, base)] = true
		}
	}
	out := make([]string, 0, len(seen))
	for label := range seen {
		out = append(out, label)
	}
	sort.Strings(out)
	return out
}

// embedded reports whether a font descriptor carries a copy of the font file.
// A nil descriptor carries nothing; the placeholder descriptor a Type 3 font
// gets never does either.
func embedded(fd *pdf.Dict) bool {
	if fd == nil {
		return false
	}
	return fd.Has("FontFile") || fd.Has("FontFile2") || fd.Has("FontFile3")
}

// fontLabel is the name a font is reported under: its PostScript name when the
// font dictionary carries one, otherwise the resource dictionary key.
func fontLabel(key, base string) string {
	if base != "" {
		return base
	}
	return key
}

// fontDescriptor resolves a font value to its descriptor and its PostScript
// name, following the Type 0 and CID font wrappers producers use.
func fontDescriptor(f *pdf.File, v pdf.Object, depth int) (*pdf.Dict, string) {
	if depth > 6 {
		return nil, ""
	}
	d, _ := f.Object(refNum(v)).(*pdf.Dict)
	if d == nil {
		d, _ = v.(*pdf.Dict)
	}
	if d == nil {
		return nil, ""
	}
	switch d.NameValue("Subtype") {
	case "Type0":
		for _, kid := range d.ArrayValue("DescendantFonts") {
			if fd, name := fontDescriptor(f, kid, depth+1); fd != nil {
				return fd, name
			}
		}
		return nil, fontName(d)
	case "Type3":
		// A Type 3 font draws its own glyphs, so there is nothing to embed.
		return pdf.NewDict(), fontName(d)
	}
	fd := d.DictValue("FontDescriptor", f)
	return fd, fontName(d)
}

// fontName reads /BaseFont. It is a name in a well-formed file, which is why
// reading it as a text string alone reports every font by its resource key
// instead of the name a reader would recognise.
func fontName(d *pdf.Dict) string {
	if n := d.NameValue("BaseFont"); n != "" {
		return decodeText(n)
	}
	return decodeText(d.StringValue("BaseFont"))
}

// pages returns the page dictionaries.
func pages(f *pdf.File, node *pdf.Dict, depth int) []*pdf.Dict {
	if depth > 32 {
		return nil
	}
	var out []*pdf.Dict
	for _, k := range node.ArrayValue("Kids") {
		r, ok := k.(pdf.Ref)
		if !ok {
			continue
		}
		child, _ := f.Object(r.Num).(*pdf.Dict)
		if child == nil {
			continue
		}
		if child.TypeName() == "Page" {
			out = append(out, child)
			continue
		}
		out = append(out, pages(f, child, depth+1)...)
	}
	return out
}

// inheritedResources walks up to the page tree when a page relies on resources
// declared by its parent, which is legal and common in older producers.
func inheritedResources(f *pdf.File, page *pdf.Dict, depth int) *pdf.Dict {
	if depth > 8 {
		return nil
	}
	r, ok := page.Get("Parent").(pdf.Ref)
	if !ok {
		return nil
	}
	parent, _ := f.Object(r.Num).(*pdf.Dict)
	if parent == nil {
		return nil
	}
	if res := parent.DictValue("Resources", f); res != nil {
		return res
	}
	return inheritedResources(f, parent, depth+1)
}
