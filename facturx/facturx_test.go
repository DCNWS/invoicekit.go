package facturx

import (
	"bytes"
	"crypto/md5"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/dcnws/invoicekit.go/internal/pdf"
)

// --- sample documents -------------------------------------------------------
//
// The XML here is written out by hand rather than produced by the generator in
// the root package: facturx sits below that package, and importing it
// back up would be a cycle. What matters for these tests is the context
// parameter, which is exactly the part a generator can get wrong, so spelling
// it out also documents the shape readers expect.

const ciiBasic = `<?xml version="1.0" encoding="UTF-8"?>
<rsm:CrossIndustryInvoice xmlns:rsm="urn:un:unece:uncefact:data:standard:CrossIndustryInvoice:100"
  xmlns:ram="urn:un:unece:uncefact:data:standard:ReusableAggregateBusinessInformationEntity:100">
  <rsm:ExchangedDocumentContext>
    <ram:BusinessProcessSpecifiedDocumentContextParameter>
      <ram:ID>urn:www.cncl.nl:validvalues:businessprocess:delivery</ram:ID>
    </ram:BusinessProcessSpecifiedDocumentContextParameter>
    <ram:GuidelineSpecifiedDocumentContextParameter>
      <ram:ID>urn:cen.eu:en16931:2017#compliant#urn:factur-x.eu:1p0:basic</ram:ID>
    </ram:GuidelineSpecifiedDocumentContextParameter>
  </rsm:ExchangedDocumentContext>
  <rsm:ExchangedDocument>
    <ram:ID>INV-2026-0142</ram:ID>
    <ram:TypeCode>380</ram:TypeCode>
  </rsm:ExchangedDocument>
  <ram:SupplyChainTradeTransaction>
    <ram:ApplicableHeaderTradeAgreement>
      <ram:BuyerReference>991-abcdef</ram:BuyerReference>
    </ram:ApplicableHeaderTradeAgreement>
  </ram:SupplyChainTradeTransaction>
</rsm:CrossIndustryInvoice>`

var ciiXRechnung = strings.Replace(ciiBasic,
	"urn:cen.eu:en16931:2017#compliant#urn:factur-x.eu:1p0:basic",
	"urn:cen.eu:en16931:2017#compliant#urn:xeinkauf.de:kosit:xrechnung_3.0", 1)

// ciiMinimum puts the context parameter first in the document, which is what
// the standard's own examples do, and carries the minimum profile's URN.
const ciiMinimum = `<?xml version="1.0" encoding="UTF-8"?>
<rsm:CrossIndustryInvoice xmlns:rsm="urn:un:unece:uncefact:data:standard:CrossIndustryInvoice:100"
  xmlns:ram="urn:un:unece:uncefact:data:standard:ReusableAggregateBusinessInformationEntity:100">
  <rsm:ExchangedDocumentContext>
    <ram:GuidelineSpecifiedDocumentContextParameter>
      <ram:ID>urn:factur-x.eu:1p0:minimum</ram:ID>
    </ram:GuidelineSpecifiedDocumentContextParameter>
  </rsm:ExchangedDocumentContext>
  <rsm:ExchangedDocument><ram:ID>INV-2026-0143</ram:ID></rsm:ExchangedDocument>
</rsm:CrossIndustryInvoice>`

// ciiNoGuideline is legal CII and illegal Factur-X: without BT-24 nothing says
// which profile the document is written to.
const ciiNoGuideline = `<?xml version="1.0" encoding="UTF-8"?>
<rsm:CrossIndustryInvoice xmlns:rsm="urn:un:unece:uncefact:data:standard:CrossIndustryInvoice:100"
  xmlns:ram="urn:un:unece:uncefact:data:standard:ReusableAggregateBusinessInformationEntity:100">
  <rsm:ExchangedDocumentContext>
    <ram:BusinessProcessSpecifiedDocumentContextParameter>
      <ram:ID>urn:www.cncl.nl:validvalues:businessprocess:delivery</ram:ID>
    </ram:BusinessProcessSpecifiedDocumentContextParameter>
  </rsm:ExchangedDocumentContext>
  <rsm:ExchangedDocument><ram:ID>INV-2026-0144</ram:ID></rsm:ExchangedDocument>
</rsm:CrossIndustryInvoice>`

// orderxCii uses the Order-X root element, which shares the CII vocabulary but
// declares the SCRDM transaction.
const orderxCii = `<?xml version="1.0" encoding="UTF-8"?>
<rsm:CrossIndustryDocument xmlns:rsm="urn:un:unece:uncefact:data:SCRDMCCBDACIOMessageStructure:100"
  xmlns:ram="urn:un:unece:uncefact:data:standard:ReusableAggregateBusinessInformationEntity:120">
  <rsm:ExchangedDocumentContext>
    <ram:GuidelineSpecifiedDocumentContextParameter>
      <ram:ID>urn:order-x.eu:1p0:comfort</ram:ID>
    </ram:GuidelineSpecifiedDocumentContextParameter>
  </rsm:ExchangedDocumentContext>
  <rsm:ExchangedDocument><ram:ID>PO-2026-0007</ram:ID><ram:TypeCode>220</ram:TypeCode></rsm:ExchangedDocument>
</rsm:CrossIndustryDocument>`

// zugferd1 is the retired 1.0 spelling: a different root namespace and the
// context parameter under SpecifiedExchangedDocumentContext.
const zugferd1 = `<?xml version="1.0" encoding="UTF-8"?>
<rsm:CrossIndustryDocument xmlns:rsm="urn:ferd:CrossIndustryDocument:invoice:1p0"
  xmlns:ram="urn:un:unece:uncefact:data:standard:ReusableAggregateBusinessInformationEntity:12">
  <rsm:SpecifiedExchangedDocumentContext>
    <ram:GuidelineSpecifiedDocumentContextParameter>
      <ram:ID>urn:ferd:CrossIndustryDocument:invoice:1p0:comfort</ram:ID>
    </ram:GuidelineSpecifiedDocumentContextParameter>
  </rsm:SpecifiedExchangedDocumentContext>
  <rsm:HeaderExchangedDocument><ram:ID>ZF-2013-0001</ram:ID></rsm:HeaderExchangedDocument>
</rsm:CrossIndustryDocument>`

// ublPeppol is a Peppol BIS invoice. The profile identifier (BT-23) is a Peppol
// URN that says nothing about Factur-X; the customisation identifier (BT-24) is
// the one that resolves to EN 16931.
const ublPeppol = `<?xml version="1.0" encoding="UTF-8"?>
<Invoice xmlns="urn:oasis:names:specification:ubl:schema:xsd:Invoice-2"
  xmlns:cbc="urn:oasis:names:specification:ubl:schema:xsd:CommonBasicComponents-2">
  <cbc:CustomizationID>urn:cen.eu:en16931:2017#compliant#urn:fdc:peppol.eu:2017:poacc:billing:01:1.0</cbc:CustomizationID>
  <cbc:ProfileID>urn:fdc:peppol.eu:2017:poacc:billing:01:1.0</cbc:ProfileID>
  <cbc:ID>MUT-2026-114</cbc:ID>
</Invoice>`

// samplePDF builds a one-page PDF from scratch. embeddedFont decides whether
// the Helvetica font travels with the document: PDF/A forbids the base-14
// fonts unless a copy is embedded, so the switch lets that check be exercised
// from both sides.
func samplePDF(t *testing.T, embeddedFont bool) []byte {
	t.Helper()
	f := pdf.New("1.7")
	root, err := f.Root()
	if err != nil {
		t.Fatalf("Root: %v", err)
	}

	content := pdf.NewStream([]byte("BT /F1 12 Tf 72 720 Td (Invoice INV-2026-0142) Tj ET"))
	if err := content.Compress(-1); err != nil {
		t.Fatalf("compress content: %v", err)
	}
	contentNum := f.Add(content)

	font := pdf.NewDict()
	font.Set("Type", pdf.Name("Font"))
	font.Set("Subtype", pdf.Name("Type1"))
	font.Set("BaseFont", pdf.Name("Helvetica"))
	font.Set("Encoding", pdf.Name("WinAnsiEncoding"))
	if embeddedFont {
		// A stub font file is enough: the audit only asks whether the
		// descriptor names one, and a real subset belongs to the producer.
		fontNum := f.Add(pdf.NewStream([]byte("OTTO fake font program")))
		fd := pdf.DictFrom(
			pdf.Name("Type"), pdf.Name("FontDescriptor"),
			pdf.Name("Ascent"), pdf.Real(718),
			pdf.Name("Descent"), pdf.Real(-207),
			pdf.Name("CapHeight"), pdf.Real(718),
			pdf.Name("Flags"), pdf.Int(32),
			pdf.Name("ItalicAngle"), pdf.Int(0),
			pdf.Name("StemV"), pdf.Real(88),
			pdf.Name("FontFile2"), pdf.Ref{Num: fontNum},
		)
		font.Set("FontDescriptor", pdf.Ref{Num: f.Add(fd)})
	}
	fontNum := f.Add(font)

	page := pdf.NewDict()
	page.Set("Type", pdf.Name("Page"))
	page.Set("MediaBox", pdf.Array{pdf.Int(0), pdf.Int(0), pdf.Int(595), pdf.Int(842)})
	page.Set("Resources", pdf.DictFrom(pdf.Name("Font"), pdf.DictFrom(pdf.Name("F1"), pdf.Ref{Num: fontNum})))
	page.Set("Contents", pdf.Ref{Num: contentNum})
	pageNum := f.Add(page)
	page.Set("Parent", pdf.Ref{Num: 0}) // replaced below once the node exists

	node := pdf.NewDict()
	node.Set("Type", pdf.Name("Pages"))
	node.Set("Count", pdf.Int(1))
	node.Set("Kids", pdf.Array{pdf.Ref{Num: pageNum}})
	nodeNum := f.Add(node)
	page.Set("Parent", pdf.Ref{Num: nodeNum})

	root.Set("Pages", pdf.Ref{Num: nodeNum})

	out, err := f.Bytes()
	if err != nil {
		t.Fatalf("write sample: %v", err)
	}
	if _, err := pdf.Parse(out); err != nil {
		t.Fatalf("sample does not parse back: %v", err)
	}
	return out
}

func fixedTime() time.Time { return time.Date(2026, 3, 4, 9, 30, 0, 0, time.UTC) }

// --- profiles ---------------------------------------------------------------

func TestParseProfile(t *testing.T) {
	ok := []struct {
		in   string
		want Profile
	}{
		{"en16931", Profile{FacturX, EN16931}},
		{"minimum", Profile{FacturX, Minimum}},
		{"basic wl", Profile{FacturX, BasicWL}},
		{"BASIC-WL", Profile{FacturX, BasicWL}},
		{"basicwl", Profile{FacturX, BasicWL}},
		{"factur-x/extended", Profile{FacturX, Extended}},
		{"factur-x en 16931", Profile{FacturX, EN16931}},
		{"Factur-X EN 16931", Profile{FacturX, EN16931}},
		{"zugferd comfort", Profile{ZUGFeRD, Comfort}},
		{"zugferd/extended", Profile{ZUGFeRD, Extended}},
		{"order-x comfort", Profile{OrderX, Comfort}},
		{"orderx/extended", Profile{OrderX, Extended}},
		{"order x extended", Profile{OrderX, Extended}},
	}
	for _, tc := range ok {
		got, err := ParseProfile(tc.in)
		if err != nil {
			t.Errorf("ParseProfile(%q): %v", tc.in, err)
			continue
		}
		if got != tc.want {
			t.Errorf("ParseProfile(%q) = %v, want %v", tc.in, got, tc.want)
		}
	}
	// A bare level is read as Factur-X, so a level Factur-X does not have is
	// ambiguous rather than a hint at another flavour: comfort belongs to both
	// Order-X and ZUGFeRD, and guessing wrong writes the wrong document.
	bad := []string{"", "nonsense", "factur-x/nonsense", "zugferd/comfort-plus", "order-x/minimum", "factur-x/comfort", "comfort"}
	for _, in := range bad {
		if got, err := ParseProfile(in); err == nil {
			t.Errorf("ParseProfile(%q) = %v, want an error", in, got)
		}
	}
}

func TestProfileDerivedValues(t *testing.T) {
	for _, p := range Profiles() {
		if err := p.Validate(); err != nil {
			t.Errorf("%v does not validate: %v", p, err)
		}
		if p.Filename() == "" || p.URN() == "" || p.Prefix() == "" || p.XMPLevel() == "" || p.DocType() == "" {
			t.Errorf("%v is missing a derived value: name %q urn %q prefix %q level %q doctype %q",
				p, p.Filename(), p.URN(), p.Prefix(), p.XMPLevel(), p.DocType())
		}
		if !strings.Contains(strings.ToLower(p.String()), strings.ToLower(p.XMPLevel())) {
			t.Errorf("%v renders as %q, which hides the level", p, p.String())
		}
		// Every name, URN and level has to be reachable from the packet a
		// producer writes, or a document cannot be identified after reading.
		back, err := ParseProfile(string(p.Flavor) + "/" + strings.ToLower(p.XMPLevel()))
		if err != nil {
			t.Errorf("profile %v cannot be parsed back from %q: %v", p, p.XMPLevel(), err)
			continue
		}
		if back != p {
			t.Errorf("round trip of %v through %q gave %v", p, p.XMPLevel(), back)
		}
	}
	if n := len(Profiles()); n != 11 {
		t.Errorf("Profiles() returned %d, want 11", n)
	}
	shown := map[Profile]string{
		{FacturX, EN16931}: "Factur-X EN 16931",
		{FacturX, BasicWL}: "Factur-X BASIC WL",
		{OrderX, Comfort}:  "Order-X Comfort",
		{ZUGFeRD, Basic}:   "ZUGFeRD 1.0 Basic",
		{FacturX, Minimum}: "Factur-X Minimum",
	}
	for p, want := range shown {
		if got := p.String(); got != want {
			t.Errorf("%v.String() = %q, want %q", p, got, want)
		}
	}
	if err := (Profile{FacturX, Comfort}).Validate(); err == nil {
		t.Error("Factur-X Comfort should not validate: that level belongs to Order-X and ZUGFeRD")
	}
	if err := (Profile{Flavour("nothing"), Basic}).Validate(); err == nil {
		t.Error("an unknown flavour should not validate")
	}
}

// --- detection --------------------------------------------------------------

func TestDetectProfile(t *testing.T) {
	cases := []struct {
		name string
		doc  string
		want Profile
		bad  bool
	}{
		{"cii basic", ciiBasic, Profile{FacturX, Basic}, false},
		{"cii minimum", ciiMinimum, Profile{FacturX, Minimum}, false},
		{"xrechnung customisation falls back to its base", ciiXRechnung, Profile{FacturX, EN16931}, false},
		{"order-x", orderxCii, Profile{OrderX, Comfort}, false},
		{"zugferd 1.0 nesting", zugferd1, Profile{ZUGFeRD, Comfort}, false},
		{"ubl peppol reads CustomizationID", ublPeppol, Profile{FacturX, EN16931}, false},
		// The business process URN is BT-23, not BT-24: reading it as the
		// guideline would make every Factur-X document look unidentified.
		{"no guideline leaves the level empty", ciiNoGuideline, Profile{FacturX, ""}, false},
		{"other syntax", `<invoice xmlns="urn:example:invoice"><id>1</id></invoice>`, Profile{}, true},
		{"not XML", `this is not XML`, Profile{}, true},
		{"empty", ``, Profile{}, true},
	}
	for _, tc := range cases {
		got, err := DetectProfile([]byte(tc.doc))
		if tc.bad {
			if err == nil {
				t.Errorf("%s: want an error, got %v", tc.name, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("%s: %v", tc.name, err)
			continue
		}
		if got != tc.want {
			t.Errorf("%s: got %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestDetectProfilePrefersTheRootSyntax(t *testing.T) {
	// A CII invoice whose BT-24 names an Order-X profile is a broken
	// document. The guideline still carries the level, so it is reported,
	// but the mismatch is what a caller needs to see.
	doc := strings.Replace(ciiBasic, "urn:cen.eu:en16931:2017#compliant#urn:factur-x.eu:1p0:basic", "urn:order-x.eu:1p0:comfort", 1)
	got, err := DetectProfile([]byte(doc))
	if err != nil {
		t.Fatalf("DetectProfile: %v", err)
	}
	if got != (Profile{OrderX, Comfort}) {
		t.Errorf("got %v, want order-x comfort", got)
	}
}

// --- embedding --------------------------------------------------------------

func embedOptions() Options {
	return Options{
		Now: fixedTime(),
		Metadata: Metadata{
			Title:       "Invoice INV-2026-0142",
			Author:      "Northwind Trade Ltd",
			Subject:     "Invoice to Contoso Ltd for March 2026",
			Keywords:    "invoice, INV-2026-0142",
			CreatorTool: "einvoice tests",
			Lang:        "en-GB",
		},
		Attachments: []Attachment{{
			Filename: "line-detail.csv",
			Data:     []byte("line,quantity,amount\n1,3,120.00\n2,1,45.00\n"),
			Desc:     "line detail for the recipient's ledger",
		}},
	}
}

func TestEmbedInspectRoundTrip(t *testing.T) {
	src := samplePDF(t, true)
	xmlData := []byte(ciiBasic)
	opts := embedOptions()

	out, err := Embed(src, xmlData, opts)
	if err != nil {
		t.Fatalf("Embed: %v", err)
	}
	doc, err := Inspect(out)
	if err != nil {
		t.Fatalf("Inspect: %v", err)
	}

	if doc.Profile != (Profile{FacturX, Basic}) {
		t.Errorf("envelope profile = %v, want Factur-X Basic", doc.Profile)
	}
	if doc.Declared != (Profile{FacturX, Basic}) {
		t.Errorf("payload profile = %v, want Factur-X Basic", doc.Declared)
	}
	if doc.XMLFilename != "factur-x.xml" {
		t.Errorf("payload attached as %q, want factur-x.xml", doc.XMLFilename)
	}
	if !bytes.Equal(doc.XML, xmlData) {
		t.Errorf("payload round trip differs: %d bytes back, %d sent", len(doc.XML), len(xmlData))
	}
	if doc.Pages != 1 {
		t.Errorf("Pages = %d, want 1", doc.Pages)
	}
	if doc.PDFVersion != "1.7" {
		t.Errorf("PDFVersion = %q, want 1.7: PDF/A-3 is defined against 1.7", doc.PDFVersion)
	}

	x := doc.XMP
	if !x.HasPacket || !x.HasSchema {
		t.Errorf("XMP packet present %v, extension declared %v", x.HasPacket, x.HasSchema)
	}
	if x.Part != 3 || x.Conf != "B" {
		t.Errorf("pdfaid = part %d conformance %q, want part 3 B", x.Part, x.Conf)
	}
	if x.DocType != "INVOICE" || x.Version != "1.0" || x.Level != "BASIC" || x.Flavor != FacturX {
		t.Errorf("hybrid properties = %q %q %q %q, want INVOICE 1.0 BASIC factur-x", x.DocType, x.Version, x.Level, x.Flavor)
	}
	if x.FileName != "factur-x.xml" {
		t.Errorf("fx:DocumentFileName = %q", x.FileName)
	}
	if x.Title != opts.Metadata.Title || x.Creator != opts.Metadata.Author || x.Description != opts.Metadata.Subject {
		t.Errorf("descriptive metadata lost: %q / %q / %q", x.Title, x.Creator, x.Description)
	}
	if doc.Metadata.Title != opts.Metadata.Title || doc.Metadata.Lang != "" && doc.Metadata.Producer != DefaultProducer {
		t.Errorf("document metadata = %+v", doc.Metadata)
	}
	if !doc.Metadata.Created.Equal(fixedTime()) {
		t.Errorf("CreateDate = %v, want %v", doc.Metadata.Created, fixedTime())
	}
	if !doc.Metadata.Modified.Equal(fixedTime()) {
		t.Errorf("ModDate = %v, want %v", doc.Metadata.Modified, fixedTime())
	}

	if len(doc.Files) != 2 {
		t.Fatalf("Files = %d entries %v, want the payload and the CSV", len(doc.Files), doc.Files)
	}
	want := []FileEntry{
		{Filename: "factur-x.xml", MIME: "text/xml", Relationship: "Data", Size: len(xmlData), IsXML: true},
		{Filename: "line-detail.csv", MIME: "text/csv", Relationship: "Supplement", Size: len(opts.Attachments[0].Data)},
	}
	for i, w := range want {
		got := doc.Files[i]
		if got.Filename != w.Filename || got.MIME != w.MIME || got.Relationship != w.Relationship || got.Size != w.Size || got.IsXML != w.IsXML {
			t.Errorf("file %d = %+v, want %+v", i, got, w)
		}
		if len(got.RecordedMD5) != 16 {
			t.Errorf("file %q has a %d-byte digest, want MD5", got.Filename, len(got.RecordedMD5))
		}
		if !got.DigestOK {
			t.Errorf("file %q does not match its own digest", got.Filename)
		}
	}

	payload, prof, err := Extract(out)
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if !bytes.Equal(payload, xmlData) || prof != (Profile{FacturX, Basic}) {
		t.Errorf("Extract returned %d bytes for %v", len(payload), prof)
	}
}

func TestExtractRejectsPlainPDF(t *testing.T) {
	if _, _, err := Extract(samplePDF(t, true)); err == nil {
		t.Error("Extract on a PDF without a payload should fail; Inspect is the call that tolerates one")
	}
	doc, err := Inspect(samplePDF(t, true))
	if err != nil {
		t.Fatalf("Inspect on a plain PDF: %v", err)
	}
	if len(doc.XML) != 0 || len(doc.Files) != 0 {
		t.Errorf("a plain PDF came back with payload or files: %d bytes, %d files", len(doc.XML), len(doc.Files))
	}
	if doc.XMP.HasPacket {
		t.Error("a plain PDF should report no XMP packet")
	}
}

func TestEmbedRefusesAnAlreadyHybridPDF(t *testing.T) {
	// Wrapping a hybrid document would leave two attachments with the same
	// name, and a reader takes whichever it finds first. The second wrap is
	// therefore refused rather than written.
	first, err := Embed(samplePDF(t, true), []byte(ciiBasic), embedOptions())
	if err != nil {
		t.Fatalf("first Embed: %v", err)
	}
	out, err := Embed(first, []byte(ciiBasic), embedOptions())
	if err == nil {
		t.Fatalf("second Embed wrote %d bytes, want a refusal", len(out))
	}
	if !strings.Contains(err.Error(), "already carries") {
		t.Errorf("error is %q, want it to name the attachment already present", err)
	}
}

func TestAuditFlagsASecondXMLAttachment(t *testing.T) {
	// Two XML attachments is one too many even when both parse: the recipient
	// cannot tell which invoice the pages belong to.
	out, err := Embed(samplePDF(t, true), []byte(ciiBasic), Options{
		Now: fixedTime(),
		Attachments: []Attachment{{
			Filename: "another-invoice.xml",
			Data:     []byte(ciiMinimum),
			MIME:     "text/xml",
			Desc:     "a second invoice XML",
		}},
	})
	if err != nil {
		t.Fatalf("Embed: %v", err)
	}
	_, rep, err := Audit(out)
	if err != nil {
		t.Fatalf("Audit: %v", err)
	}
	if !hasRule(rep, "fx.multipayload") {
		t.Errorf("expected fx.multipayload, got:\n%s", rep)
	}
}

func TestEmbedRejectsMismatchedProfiles(t *testing.T) {
	src := samplePDF(t, true)
	cases := []struct {
		name string
		opts Options
		doc  string
	}{
		{"order-x asked for an invoice", Options{Profile: Profile{OrderX, Comfort}}, ciiBasic},
		{"level disagrees with BT-24", Options{Profile: Profile{FacturX, Extended}}, ciiBasic},
		{"level that flavour does not have", Options{Profile: Profile{FacturX, Comfort}}, ciiBasic},
		{"payload declares no profile", Options{}, ciiNoGuideline},
		{"not a cross-industry document", Options{}, `<invoice xmlns="urn:example:invoice"/>`},
	}
	for _, tc := range cases {
		if out, err := Embed(src, []byte(tc.doc), tc.opts); err == nil {
			t.Errorf("%s: want an error, got %d bytes", tc.name, len(out))
		}
	}
}

func TestEmbedAcceptsExplicitProfile(t *testing.T) {
	// The payload says nothing about its profile, so the caller has to state
	// one; the envelope then carries it and the audit notes the gap.
	out, err := Embed(samplePDF(t, true), []byte(ciiNoGuideline), Options{
		Profile: Profile{FacturX, Basic},
		Now:     fixedTime(),
	})
	if err != nil {
		t.Fatalf("Embed: %v", err)
	}
	doc, err := Inspect(out)
	if err != nil {
		t.Fatalf("Inspect: %v", err)
	}
	if doc.Profile != (Profile{FacturX, Basic}) {
		t.Errorf("envelope says %v, want Factur-X Basic", doc.Profile)
	}
	if doc.Declared.Flavor != FacturX || doc.Declared.Level != "" {
		t.Errorf("payload says %v, want factur-x with an empty level", doc.Declared)
	}
	_, rep, err := Audit(out)
	if err != nil {
		t.Fatalf("Audit: %v", err)
	}
	if !hasRule(rep, "fx.context") {
		t.Errorf("expected the missing-guideline warning, got:\n%s", rep)
	}
}

func TestEmbedDeterministic(t *testing.T) {
	src := samplePDF(t, true)
	opts := embedOptions()
	a, err := Embed(src, []byte(ciiBasic), opts)
	if err != nil {
		t.Fatalf("Embed: %v", err)
	}
	b, err := Embed(src, []byte(ciiBasic), opts)
	if err != nil {
		t.Fatalf("Embed: %v", err)
	}
	if !bytes.Equal(a, b) {
		t.Errorf("two runs over the same inputs differ (%d vs %d bytes); an archive should be able to hash its own output", len(a), len(b))
	}
	if !bytes.Contains(a, []byte(pdfDate(fixedTime()))) {
		t.Error("the fixed timestamp is missing from the output")
	}
}

func TestEmbedKeepsSourceMetadata(t *testing.T) {
	// Wrapping a PDF must not quietly discard what its author wrote.
	f := pdf.New("1.7")
	info := pdf.DictFrom(
		pdf.Name("Title"), pdf.String("Quarterly statement"),
		pdf.Name("Author"), pdf.String("Fabrikam Inc"),
		pdf.Name("Producer"), pdf.String("fabrikam-print 4.1"),
	)
	f.Trailer.Set("Info", pdf.Ref{Num: f.Add(info)})
	root, err := f.Root()
	if err != nil {
		t.Fatalf("Root: %v", err)
	}
	root.Set("Pages", pdf.Ref{Num: f.Add(pdf.DictFrom(pdf.Name("Type"), pdf.Name("Pages"), pdf.Name("Count"), pdf.Int(0)))})
	src, err := f.Bytes()
	if err != nil {
		t.Fatalf("write source: %v", err)
	}

	out, err := Embed(src, []byte(ciiBasic), Options{Now: fixedTime()})
	if err != nil {
		t.Fatalf("Embed: %v", err)
	}
	doc, err := Inspect(out)
	if err != nil {
		t.Fatalf("Inspect: %v", err)
	}
	if doc.Metadata.Title != "Quarterly statement" || doc.Metadata.Author != "Fabrikam Inc" || doc.Metadata.Producer != "fabrikam-print 4.1" {
		t.Errorf("source metadata lost: %+v", doc.Metadata)
	}
}

// --- auditing ---------------------------------------------------------------

// hasRule reports whether the audit produced a finding for the named check.
func hasRule(r *Report, rule string) bool {
	for _, f := range r.Findings {
		if f.Rule == rule {
			return true
		}
	}
	return false
}

func findingsAt(r *Report, sev Severity) []string {
	var out []string
	for _, f := range r.Findings {
		if f.Severity == sev {
			out = append(out, f.Rule)
		}
	}
	return out
}

func TestAuditPlainPDF(t *testing.T) {
	doc, rep, err := Audit(samplePDF(t, false))
	if err != nil {
		t.Fatalf("Audit: %v", err)
	}
	if rep.OK() {
		t.Errorf("an ordinary PDF audited as acceptable:\n%s", rep)
	}
	for _, rule := range []string{"pdfa.xmp", "pdfa.outputintent", "fx.payload", "pdfa.font", "pdfa.tagged"} {
		if !hasRule(rep, rule) {
			t.Errorf("no finding for %s; got %v", rule, rep.Counts())
		}
	}
	for _, rule := range []string{"pdf.lang", "pdf.doctitle"} {
		if !hasRule(rep, rule) {
			t.Errorf("no warning for %s", rule)
		}
	}
	for _, f := range rep.Findings {
		if f.Rule == "pdfa.font" && !strings.Contains(f.Message, "Helvetica") {
			t.Errorf("the font finding does not name the font: %q", f.Message)
		}
	}
	if doc.Pages != 1 {
		t.Errorf("Pages = %d, want 1", doc.Pages)
	}
	if rep.Counts()[SeverityError] < 4 {
		t.Errorf("counts = %v, want at least four errors", rep.Counts())
	}
	if s := rep.String(); !strings.Contains(s, "error") || !strings.HasSuffix(s, "\n") {
		t.Errorf("rendered report looks wrong:\n%s", s)
	}
}

func TestAuditHybridIsClean(t *testing.T) {
	out, err := Embed(samplePDF(t, true), []byte(ciiBasic), embedOptions())
	if err != nil {
		t.Fatalf("Embed: %v", err)
	}
	doc, rep, err := Audit(out)
	if err != nil {
		t.Fatalf("Audit: %v", err)
	}
	if !rep.OK() {
		t.Errorf("a document this package wrote failed its own audit:\n%s", rep)
	}
	if w := findingsAt(rep, SeverityWarning); len(w) > 0 {
		t.Errorf("a document this package wrote produced warnings: %v\n%s", w, rep)
	}
	if doc.XMP.Part != 3 {
		t.Errorf("pdfaid:part = %d", doc.XMP.Part)
	}
}

func TestAuditLevelAneedsMarkup(t *testing.T) {
	// Claiming level A without a tagged document is the mistake PDF/A
	// validators are least forgiving about.
	claimed, err := Embed(samplePDF(t, true), []byte(ciiBasic), Options{Conformance: "A", Now: fixedTime()})
	if err != nil {
		t.Fatalf("Embed: %v", err)
	}
	_, rep, err := Audit(claimed)
	if err != nil {
		t.Fatalf("Audit: %v", err)
	}
	if rep.OK() {
		t.Errorf("level A claimed on an untagged document and nothing objected:\n%s", rep)
	}
	for _, rule := range []string{"pdfa.tagged", "pdfa.structtree"} {
		if !hasRule(rep, rule) {
			t.Errorf("no finding for %s", rule)
		}
	}

	tagged, err := Embed(samplePDF(t, true), []byte(ciiBasic), Options{Conformance: "A", Tagged: true, Now: fixedTime()})
	if err != nil {
		t.Fatalf("Embed tagged: %v", err)
	}
	_, rep, err = Audit(tagged)
	if err != nil {
		t.Fatalf("Audit tagged: %v", err)
	}
	if !rep.OK() {
		t.Errorf("a tagged level A document failed:\n%s", rep)
	}
}

func TestAuditSpotsAlteredPayload(t *testing.T) {
	out, err := Embed(samplePDF(t, true), []byte(ciiBasic), embedOptions())
	if err != nil {
		t.Fatalf("Embed: %v", err)
	}
	f, err := pdf.Parse(out)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	root, err := f.Root()
	if err != nil {
		t.Fatalf("Root: %v", err)
	}
	poked := 0
	for _, e := range embeddedFileNames(f, root) {
		if !strings.EqualFold(e.name, "factur-x.xml") {
			continue
		}
		spec, _ := f.Object(refNum(e.spec)).(*pdf.Dict)
		if spec == nil {
			continue
		}
		ef := spec.DictValue("EF", f)
		if ef == nil {
			continue
		}
		s := ef.StreamValue("F", f)
		if s == nil {
			continue
		}
		params := s.Dict.DictValue("Params", f)
		if params == nil {
			continue
		}
		// The digest of a document that was never this one: exactly what an
		// alteration in transit leaves behind.
		wrong := md5.Sum([]byte("<rsm:CrossIndustryInvoice/>"))
		params.Set("CheckSum", pdf.String(wrong[:]))
		poked++
	}
	if poked == 0 {
		t.Fatal("could not find the payload's file specification to tamper with")
	}
	altered, err := f.Bytes()
	if err != nil {
		t.Fatalf("rewrite: %v", err)
	}

	doc, err := Inspect(altered)
	if err != nil {
		t.Fatalf("Inspect altered: %v", err)
	}
	var entry *FileEntry
	for i := range doc.Files {
		if doc.Files[i].IsXML {
			entry = &doc.Files[i]
		}
	}
	if entry == nil {
		t.Fatal("the altered document no longer reports a payload")
	}
	if entry.DigestOK {
		t.Error("DigestOK is true although the recorded digest was replaced")
	}
	_, rep, err := Audit(altered)
	if err != nil {
		t.Fatalf("Audit altered: %v", err)
	}
	if rep.OK() || !hasRule(rep, "fx.digest") {
		t.Errorf("an altered payload passed the audit:\n%s", rep)
	}
}

func TestAuditMissingFileParams(t *testing.T) {
	out, err := Embed(samplePDF(t, true), []byte(ciiBasic), embedOptions())
	if err != nil {
		t.Fatalf("Embed: %v", err)
	}
	f, err := pdf.Parse(out)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	root, err := f.Root()
	if err != nil {
		t.Fatalf("Root: %v", err)
	}
	stripped := 0
	for _, e := range embeddedFileNames(f, root) {
		spec, _ := f.Object(refNum(e.spec)).(*pdf.Dict)
		if spec == nil {
			continue
		}
		ef := spec.DictValue("EF", f)
		if ef == nil {
			continue
		}
		for _, key := range []string{"F", "UF"} {
			s := ef.StreamValue(key, f)
			if s == nil {
				continue
			}
			if s.Dict.Has("Params") {
				s.Dict.Delete("Params")
				stripped++
			}
		}
	}
	if stripped == 0 {
		t.Fatal("no /Params to strip; Embed stopped declaring file parameters")
	}
	hollow, err := f.Bytes()
	if err != nil {
		t.Fatalf("rewrite: %v", err)
	}
	_, rep, err := Audit(hollow)
	if err != nil {
		t.Fatalf("Audit: %v", err)
	}
	if rep.OK() || !hasRule(rep, "pdfa.fileparams") {
		t.Errorf("an embedded file with no digest passed the audit:\n%s", rep)
	}
}

func TestAuditReportsEnvelopeMismatch(t *testing.T) {
	// A Factur-X envelope around an Order-X payload: both halves are legal,
	// the pair is not.
	out, err := Embed(samplePDF(t, true), []byte(ciiBasic), Options{Profile: Profile{FacturX, Extended}, Now: fixedTime()})
	if err != nil {
		// Embed refuses the combination up front, which is the better
		// outcome; the audit rule stays as a safety net for other producers.
		if !strings.Contains(err.Error(), "en16931") && !strings.Contains(err.Error(), "extended") {
			t.Fatalf("Embed: %v", err)
		}
		return
	}
	_, rep, err := Audit(out)
	if err != nil {
		t.Fatalf("Audit: %v", err)
	}
	if rep.OK() || !hasRule(rep, "fx.level") {
		t.Errorf("envelope and payload disagree and nothing said so:\n%s", rep)
	}
}

// --- attachments ------------------------------------------------------------

func TestAttachmentRules(t *testing.T) {
	src := samplePDF(t, true)
	cases := []struct {
		name string
		att  Attachment
	}{
		{"empty name", Attachment{Filename: "", Data: []byte("x")}},
		{"path", Attachment{Filename: "../escape.xml", Data: []byte("x")}},
		{"directory", Attachment{Filename: "sub/thing.csv", Data: []byte("x")}},
		{"same name as the payload", Attachment{Filename: "FACTUR-X.xml", Data: []byte("x")}},
		{"unknown relationship", Attachment{Filename: "ok.csv", Data: []byte("x"), Relationship: "Attached"}},
	}
	for _, tc := range cases {
		opts := embedOptions()
		opts.Attachments = append(opts.Attachments, tc.att)
		if out, err := Embed(src, []byte(ciiBasic), opts); err == nil {
			t.Errorf("%s: accepted, %d bytes", tc.name, len(out))
		}
	}

	// Duplicates are refused whichever order they arrive in.
	opts := embedOptions()
	opts.Attachments = append(opts.Attachments,
		Attachment{Filename: "notes.txt", Data: []byte("one")},
		Attachment{Filename: "notes.txt", Data: []byte("two")})
	if _, err := Embed(src, []byte(ciiBasic), opts); err == nil {
		t.Error("two attachments named notes.txt were accepted")
	}

	// A PDF attachment gets its MIME type from the extension, and a payload
	// is never confused with an supporting document.
	opts = embedOptions()
	opts.Attachments = append(opts.Attachments, Attachment{Filename: "scan.pdf", Data: samplePDF(t, false)})
	out, err := Embed(src, []byte(ciiBasic), opts)
	if err != nil {
		t.Fatalf("Embed: %v", err)
	}
	doc, err := Inspect(out)
	if err != nil {
		t.Fatalf("Inspect: %v", err)
	}
	if len(doc.Files) != 3 {
		t.Fatalf("Files = %d, want 3", len(doc.Files))
	}
	for _, fe := range doc.Files {
		switch fe.Filename {
		case "scan.pdf":
			if fe.MIME != "application/pdf" || fe.Relationship != "Supplement" || !fe.DigestOK {
				t.Errorf("scan.pdf = %+v", fe)
			}
			if fe.IsXML {
				t.Error("a PDF was taken for the invoice payload")
			}
		case "factur-x.xml":
			if fe.Relationship != "Data" || !fe.IsXML {
				t.Errorf("payload entry = %+v", fe)
			}
		}
	}
}

// --- text, dates and the report ---------------------------------------------

func TestDecodeText(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"ascii", "Invoice INV-2026-0142", "Invoice INV-2026-0142"},
		{"utf16 big endian", "\xfe\xff\x00H\x00i\x00 \x00\xe9", "Hi é"},
		{"utf16 little endian", "\xff\xfeH\x00i\x00 \x00\xe9\x00", "Hi é"},
		{"utf8 with byte order mark", "\xef\xbb\xbfInvité", "Invité"},
		{"latin-1 upper half", "C\xe9line M\xfcller", "Céline Müller"},
		{"cp1252 punctuation", "\x93quoted\x94\x96\x85\x80", "“quoted”–…€"},
		{"empty", "", ""},
		// A trailing half unit is dropped rather than turning into NUL.
		{"truncated utf16", "\xfe\xff\x00", ""},
	}
	for _, tc := range tests {
		if got := decodeText(tc.in); got != tc.want {
			t.Errorf("%s: decodeText(%q) = %q, want %q", tc.name, tc.in, got, tc.want)
		}
	}
}

func TestPDFTime(t *testing.T) {
	// The full form, which is what producers write: note the quoted offsets.
	got := pdfTime("D:20260214090000+01'00'")
	if got.UTC().Format(time.RFC3339) != "2026-02-14T08:00:00Z" {
		t.Errorf("offset time = %s, want 2026-02-14T08:00:00Z", got.UTC().Format(time.RFC3339))
	}
	if got.Format("-0700") != "+0100" {
		t.Errorf("offset was flattened: %s", got.Format("-0700"))
	}
	if got := pdfTime("D:20260214090000-05'30'").UTC().Format(time.RFC3339); got != "2026-02-14T14:30:00Z" {
		t.Errorf("negative offset = %s", got)
	}
	if got := pdfTime("D:20260214090000.5+00'00'").UTC().Format(time.RFC3339); got != "2026-02-14T09:00:00Z" {
		t.Errorf("fractional seconds = %s", got)
	}
	// Missing parts count as zero, per ISO 32000-1:2017 §7.9.4.
	if got := pdfTime("D:20260214").UTC().Format(time.RFC3339); got != "2026-02-14T00:00:00Z" {
		t.Errorf("date only = %s", got)
	}
	if got := pdfTime("20260214090000Z").UTC().Format(time.RFC3339); got != "2026-02-14T09:00:00Z" {
		t.Errorf("without the D: prefix = %s", got)
	}
	for _, s := range []string{"", "D:", "D:2026", "D:00000000", "D:20260000", "nonsense"} {
		if !pdfTime(s).IsZero() {
			t.Errorf("pdfTime(%q) should be the zero time", s)
		}
	}
}

func TestReportRendering(t *testing.T) {
	r := &Report{}
	if !r.OK() {
		t.Error("an empty report must be OK")
	}
	r.add(SeverityInfo, "fx.pages", "%d page(s)", 2)
	r.add(SeverityWarning, "fx.fonts", "font %s is not embedded", "Helvetica")
	r.add(SeverityError, "fx.digest", "checksum mismatch for %s", "factur-x.xml")
	if r.OK() {
		t.Error("a report holding an error is not OK")
	}
	if c := r.Counts(); c[SeverityError] != 1 || c[SeverityWarning] != 1 || c[SeverityInfo] != 1 {
		t.Errorf("Counts = %v", c)
	}

	// Rendering puts the errors first, since that is what a caller reads for.
	s := r.String()
	i, j, k := strings.Index(s, "fx.digest"), strings.Index(s, "fx.fonts"), strings.Index(s, "fx.pages")
	if i < 0 || j < 0 || k < 0 {
		t.Fatalf("a rule is missing from:\n%s", s)
	}
	if !(i < j && j < k) {
		t.Errorf("findings are not ordered by severity:\n%s", s)
	}
	lines := strings.Split(strings.TrimSuffix(s, "\n"), "\n")
	if len(lines) != 3 {
		t.Fatalf("%d lines, want 3:\n%s", len(lines), s)
	}
	for n, want := range []string{"error", "warning", "info"} {
		if !strings.HasPrefix(lines[n], want) {
			t.Errorf("line %d = %q, want it to start with %q", n, lines[n], want)
		}
	}
	// Sorting happens on the way out only; the audit's own order survives.
	if r.Findings[0].Rule != "fx.pages" {
		t.Errorf("Findings was reordered: %v", r.Findings)
	}
}

// --- the XMP packet ---------------------------------------------------------

func TestXMPPacketCarriesEveryProperty(t *testing.T) {
	p := Profile{Flavor: FacturX, Level: Extended}
	m := (Metadata{
		Title:       "Acme & Sons <Ltd>",
		Author:      "Northwind \"Trading\" Ltd",
		Subject:     "Invoice for March & April",
		Keywords:    "invoice, march",
		Producer:    "einvoice test producer",
		CreatorTool: "einvoice tests",
		Created:     fixedTime(),
		Modified:    fixedTime().Add(2 * time.Hour),
	}).fill(fixedTime())

	packet := xmpPacket(m, p, "B")
	info, err := readXMP(packet)
	if err != nil {
		t.Fatalf("readXMP: %v", err)
	}
	if !info.HasPacket || !info.HasSchema {
		t.Errorf("HasPacket/HasSchema = %v/%v, want both true (an undeclared property is a validation failure)", info.HasPacket, info.HasSchema)
	}
	if info.Part != 3 || info.Conf != "B" {
		t.Errorf("pdfaid = part %d conformance %q", info.Part, info.Conf)
	}
	if info.Flavor != FacturX {
		t.Errorf("Flavor = %q", info.Flavor)
	}
	if info.DocType != p.DocType() || info.FileName != p.Filename() || info.Version != p.Version() || info.Level != p.XMPLevel() {
		t.Errorf("fx properties = %+v", info)
	}
	// Escaping on the way out, unescaping on the way back in: the round trip
	// has to be exact or a recipient's ledger gets a mangled name.
	for _, tc := range []struct{ field, got, want string }{
		{"title", info.Title, m.Title},
		{"creator", info.Creator, m.Author},
		{"description", info.Description, m.Subject},
		{"keywords", info.Keywords, m.Keywords},
		{"producer", info.Producer, m.Producer},
		{"creatortool", info.CreatorTool, m.CreatorTool},
	} {
		if tc.got != tc.want {
			t.Errorf("%s = %q, want %q", tc.field, tc.got, tc.want)
		}
	}
	if info.Created != xmpDate(m.Created) || info.Modified != xmpDate(m.Modified) {
		t.Errorf("dates = %q / %q", info.Created, info.Modified)
	}
	// The legacy document information dictionary and XMP have to name the same
	// instants, so the two formats must agree on the value they hold.
	if want := "<pdfaid:part>3</pdfaid:part>"; !strings.Contains(string(packet), want) {
		t.Errorf("packet is missing %q", want)
	}
	if want := "<" + p.Prefix() + ":DocumentType>" + p.DocType() + "</" + p.Prefix() + ":DocumentType>"; !strings.Contains(string(packet), want) {
		t.Errorf("packet is missing %q", want)
	}
	if !strings.Contains(string(packet), "Acme &amp; Sons &lt;Ltd&gt;") {
		t.Error("the title was not escaped")
	}
	if strings.Count(string(packet), "\n") < 20 {
		t.Error("the packet looks truncated")
	}
}

func TestReadXMPFollowsTheURNNotThePrefix(t *testing.T) {
	// Producers use whatever prefix they like. Recognising the document by its
	// namespace URI, rather than by fx:, is what keeps Order-X and ZUGFeRD
	// readable without a table of aliases.
	packet := `<?xpacket begin=""?>
<x:xmpmeta xmlns:x="adobe:ns:meta/">
 <rdf:RDF xmlns:rdf="http://www.w3.org/1999/02/22-rdf-syntax-ns#">
  <rdf:Description rdf:about="" xmlns:pdfaid="http://www.aiim.org/pdfa/ns/id/">
   <pdfaid:part>3</pdfaid:part><pdfaid:conformance>U</pdfaid:conformance>
  </rdf:Description>
  <rdf:Description rdf:about="" xmlns:z="` + urns[FacturX] + `">
   <z:DocumentType>INVOICE</z:DocumentType>
   <z:DocumentFileName>factur-x.xml</z:DocumentFileName>
   <z:Version>1.0</z:Version>
   <z:ConformanceLevel>Basic</z:ConformanceLevel>
  </rdf:Description>
  <rdf:Description rdf:about="" xmlns:dc="http://purl.org/dc/elements/1.1/">
   <dc:title><rdf:Alt><rdf:li xml:lang="x-default">Invoice</rdf:li><rdf:li xml:lang="de">Rechnung</rdf:li></rdf:Alt></dc:title>
  </rdf:Description>
  <rdf:Description rdf:about="" xmlns:ex="http://example.com/invoice-tools#">
   <ex:InternalID>42</ex:InternalID>
  </rdf:Description>
  <rdf:Description rdf:about="" xmlns:pdfaExtension="http://www.aiim.org/pdfa/ns/extension/" xmlns:pdfaSchema="http://www.aiim.org/pdfa/ns/schema#">
   <pdfaExtension:schemas><rdf:Bag><rdf:li rdf:parseType="Resource">
    <pdfaSchema:schema>Factur-X PDFA Extension Schema</pdfaSchema:schema>
    <pdfaSchema:prefix>fx</pdfaSchema:prefix>
   </rdf:li></rdf:Bag></pdfaExtension:schemas>
  </rdf:Description>
 </rdf:RDF>
</x:xmpmeta>
<?xpacket end="w"?>`

	info, err := readXMP([]byte(packet))
	if err != nil {
		t.Fatalf("readXMP: %v", err)
	}
	if info.Flavor != FacturX {
		t.Errorf("Flavor = %q, want %q", info.Flavor, FacturX)
	}
	if info.Part != 3 || info.Conf != "U" {
		t.Errorf("pdfaid = part %d conformance %q", info.Part, info.Conf)
	}
	if info.DocType != "INVOICE" || info.FileName != "factur-x.xml" || info.Version != "1.0" || info.Level != "Basic" {
		t.Errorf("hybrid properties = %+v", info)
	}
	if info.Title != "Invoice Rechnung" {
		t.Errorf("Title = %q, want the two localisations joined", info.Title)
	}
	if !info.HasSchema {
		t.Error("the schema declaration was not seen")
	}
	// Someone else's vocabulary is nobody's business but ours: it must not
	// raise an error and must not end up in a field.
	if info.DocType == "42" {
		t.Error("a foreign property overwrote the document type")
	}
}

func TestReadXMPEdgeCases(t *testing.T) {
	// No packet at all is not an error here: the caller decides how much that
	// matters, and Audit reports it once rather than twice.
	info, err := readXMP(nil)
	if err != nil {
		t.Fatalf("empty packet: %v", err)
	}
	if info.HasPacket || info.Flavor != "" || info.Part != 0 {
		t.Errorf("empty packet gave %+v", info)
	}

	for _, s := range []string{"<>", "<a></b>", "<rdf:RDF><a></a>trailing</rdf:RDF></x>"} {
		if _, err := readXMP([]byte(s)); err == nil {
			t.Errorf("malformed packet %q was accepted", s)
		}
	}
}

// rewritePart changes the pdfaid:part a document declares. That is a single
// byte of XMP, but this package compresses the stream the packet sits in, so
// the edit goes through the object model: decompress, patch, put it back.
func rewritePart(t *testing.T, data []byte, part byte) []byte {
	t.Helper()
	const marker = "pdfaid:part>"
	f, err := pdf.Parse(data)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	root, err := f.Root()
	if err != nil {
		t.Fatalf("Root: %v", err)
	}
	ref, isRef := root.Get("Metadata").(pdf.Ref)
	if !isRef {
		t.Fatal("the catalogue has no /Metadata stream to rewrite")
	}
	src, isStream := f.Object(ref.Num).(*pdf.Stream)
	if !isStream {
		t.Fatal("the catalogue's /Metadata is not a stream")
	}
	meta, err := f.Decompress(src)
	if err != nil {
		t.Fatalf("Decompress: %v", err)
	}
	i := bytes.Index(meta.Raw, []byte(marker))
	if i < 0 {
		t.Fatal("this XMP packet has no pdfaid:part to rewrite")
	}
	patched := append([]byte(nil), meta.Raw...)
	patched[i+len(marker)] = part
	meta.Raw = patched
	f.Set(ref.Num, meta)
	out, err := f.Bytes()
	if err != nil {
		t.Fatalf("Bytes: %v", err)
	}
	return out
}

func findingFor(r *Report, rule string) (Finding, bool) {
	for _, f := range r.Findings {
		if f.Rule == rule {
			return f, true
		}
	}
	return Finding{}, false
}

// TestAuditPartDecidesWhatAnAttachmentMeans covers the PDF/A part a hybrid
// declares. PDF/A-1 forbids attachments outright (ISO 19005-1 6.1.11: no
// /EmbeddedFiles in the name dictionary, no /EF in a file specification) and
// PDF/A-2 admits them only if the embedded file is itself conformant
// (ISO 19005-2 6.8.5), so the part decides what an attachment means and the
// audit has to say so in the finding.
func TestAuditPartDecidesWhatAnAttachmentMeans(t *testing.T) {
	hybrid, err := Embed(samplePDF(t, true), []byte(ciiBasic), embedOptions())
	if err != nil {
		t.Fatalf("Embed: %v", err)
	}
	for _, tc := range []struct {
		part byte
		rule string
		want string
	}{
		{'1', "pdfa.attachments", "6.1.11"},
		{'2', "pdfa.part", "6.8.5"},
		{'0', "pdfa.part", "no pdfaid:part"},
		{'7', "pdfa.part", "not a PDF/A part"},
	} {
		_, rep, err := Audit(rewritePart(t, hybrid, tc.part))
		if err != nil {
			t.Fatalf("Audit(part %c): %v", tc.part, err)
		}
		if rep.OK() {
			t.Errorf("part %c was accepted:\n%s", tc.part, rep)
		}
		f, ok := findingFor(rep, tc.rule)
		if !ok {
			t.Errorf("part %c produced no %s finding: %v\n%s", tc.part, tc.rule, rep.Counts(), rep)
			continue
		}
		if !strings.Contains(f.Message, tc.want) {
			t.Errorf("the %s finding for part %c does not explain itself with %q: %q", tc.rule, tc.part, tc.want, f.Message)
		}
	}
}

// TestAuditPartOnePointsAtTheCompanionFile strips the attachment declaration
// out of a hybrid and declares part 1, leaving the arrangement the older
// ZUGFeRD 1.0 documents use: the XMP describes a hybrid invoice, the PDF cannot
// carry one, and the XML belongs in a file beside it. "No payload" alone would
// send a reader hunting inside a PDF that was never going to contain one.
func TestAuditPartOnePointsAtTheCompanionFile(t *testing.T) {
	hybrid, err := Embed(samplePDF(t, true), []byte(ciiBasic), embedOptions())
	if err != nil {
		t.Fatalf("Embed: %v", err)
	}
	f, err := pdf.Parse(hybrid)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	root, err := f.Root()
	if err != nil {
		t.Fatalf("Root: %v", err)
	}
	root.Delete("Names")
	root.Delete("AF")
	out, err := f.Bytes()
	if err != nil {
		t.Fatalf("Bytes: %v", err)
	}
	_, rep, err := Audit(rewritePart(t, out, '1'))
	if err != nil {
		t.Fatalf("Audit: %v", err)
	}
	for _, rule := range []string{"pdfa.part", "fx.companion", "fx.payload"} {
		if !hasRule(rep, rule) {
			t.Errorf("no finding for %s: %v\n%s", rule, rep.Counts(), rep)
		}
	}
	if f, ok := findingFor(rep, "pdfa.part"); ok && !strings.Contains(f.Message, "beside") {
		t.Errorf("the part finding does not point at the companion file: %q", f.Message)
	}
	if f, ok := findingFor(rep, "fx.companion"); ok && f.Severity != SeverityInfo {
		t.Errorf("fx.companion is %s, want info", f.Severity)
	}
	if _, ok := findingFor(rep, "pdfa.attachments"); ok {
		t.Error("pdfa.attachments fired on a document with nothing attached")
	}
}

// TestTheWireFormSpeaksForEverySurface pins the JSON every surface sends. The
// CLI's -json output, the hosted API and the browser build all encode
// CheckResult, so a rule id and a severity have to survive the trip unchanged
// and a consumer written against one surface works against the others.
func TestTheWireFormSpeaksForEverySurface(t *testing.T) {
	hybrid, err := Embed(samplePDF(t, true), []byte(ciiBasic), embedOptions())
	if err != nil {
		t.Fatalf("Embed: %v", err)
	}
	doc, rep, err := Audit(hybrid)
	if err != nil {
		t.Fatalf("Audit: %v", err)
	}
	res := Result(doc, rep)
	if res.Name != "" {
		t.Errorf("Result filled in Name %q, which belongs to the caller", res.Name)
	}
	data, err := json.Marshal(res)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	var wire map[string]any
	if err := json.Unmarshal(data, &wire); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	for _, key := range []string{"envelope", "payload", "declared", "pages", "ok"} {
		if wire[key] == nil {
			t.Errorf("the wire form has no %q field: %s", key, data)
		}
	}
	if wire["envelope"] != res.Envelope || res.Envelope == "" {
		t.Errorf("envelope = %v, want the profile the document was read as", wire["envelope"])
	}
	if wire["pages"].(float64) != float64(res.Pages) || res.Pages == 0 {
		t.Errorf("pages = %v", wire["pages"])
	}
	fs, ok := wire["findings"].([]any)
	if !ok || len(fs) == 0 {
		t.Fatalf("the sample PDF produces no findings list: %s", data)
	}
	for _, v := range fs {
		f, ok := v.(map[string]any)
		if !ok {
			t.Fatalf("finding %v is not an object", v)
		}
		for _, key := range []string{"rule", "severity", "message"} {
			if s, ok := f[key].(string); !ok || s == "" {
				t.Errorf("finding %v carries no %q", f, key)
			}
		}
		switch f["severity"] {
		case "error", "warning", "info":
		default:
			t.Errorf("severity %v is not one of error, warning, info", f["severity"])
		}
	}
	if _, _, err := Audit([]byte("not a PDF at all")); err == nil {
		t.Error("Audit accepted a file that is not a PDF")
	}
}
