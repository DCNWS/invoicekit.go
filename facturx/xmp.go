package facturx

import (
	"fmt"
	"strings"
	"time"
)

// Metadata is the human-facing description of the document. PDF/A-3 requires
// that the XMP packet and the legacy document information dictionary, when
// both are present, agree; this package writes both from one Metadata value so
// they cannot drift. Empty fields are left out rather than written as empty
// strings.
type Metadata struct {
	Title    string
	Author   string
	Subject  string
	Keywords string

	// Producer names the application that wrote the PDF, CreatorTool the one
	// that produced the content: /Producer and /Creator respectively.
	Producer    string
	CreatorTool string

	Created  time.Time
	Modified time.Time

	// Lang is the document language tag, e.g. "en-GB". It fills /Lang in the
	// catalogue, which PDF/A requires for text a reader cannot infer a
	// language from.
	Lang string
}

// DefaultProducer identifies this toolkit in the output.
const DefaultProducer = "einvoice (github.com/dcnws/invoicekit.go)"

// fill returns a copy of m with zero values replaced by defaults.
func (m Metadata) fill(now time.Time) Metadata {
	out := m
	if out.Producer == "" {
		out.Producer = DefaultProducer
	}
	if out.CreatorTool == "" {
		out.CreatorTool = out.Producer
	}
	if out.Created.IsZero() {
		out.Created = now
	}
	if out.Modified.IsZero() {
		out.Modified = out.Created
	}
	if out.Lang == "" {
		out.Lang = "en"
	}
	return out
}

// pdfDate renders t in the PDF date syntax used by /CreationDate and /ModDate.
func pdfDate(t time.Time) string {
	return "D:" + t.UTC().Format("20060102150405") + "Z"
}

// xmpDate renders t in the ISO 8601 form XMP requires. It names the same
// instant as pdfDate, which is what lets a validator compare the two.
func xmpDate(t time.Time) string {
	return t.UTC().Format(time.RFC3339)
}

var textEsc = strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;")
var quotEsc = strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;")

// xmpPacket builds the XMP metadata stream required by PDF/A-3.
//
// PDF/A has a rule that bites everyone who writes their own packet: every
// property used must be declared in a schema description inside the same
// packet. That is what the pdfaExtension block at the end does for the four
// hybrid-document properties, and getting it wrong is the most common reason a
// hybrid invoice fails validation.
func xmpPacket(m Metadata, p Profile, pdfaConf string) []byte {
	var b strings.Builder
	b.WriteString("<?xpacket begin=\"\ufeff\" id=\"W5M0MpCehiHzreSzNTczkc9d\"?>\n")
	b.WriteString("<x:xmpmeta xmlns:x=\"adobe:ns:meta/\" x:xmptk=\"" + quotEsc.Replace(DefaultProducer) + "\">\n")
	b.WriteString(" <rdf:RDF xmlns:rdf=\"http://www.w3.org/1999/02/22-rdf-syntax-ns#\">\n")

	// pdfaid: part 3 (PDF/A-3) plus the conformance flavour asked for.
	fmt.Fprintf(&b, "  <rdf:Description rdf:about=\"\" xmlns:pdfaid=\"http://www.aiim.org/pdfa/ns/id/\">\n")
	fmt.Fprintf(&b, "   <pdfaid:part>3</pdfaid:part>\n   <pdfaid:conformance>%s</pdfaid:conformance>\n  </rdf:Description>\n", pdfaConf)

	if m.Title != "" || m.Subject != "" {
		b.WriteString("  <rdf:Description rdf:about=\"\" xmlns:dc=\"http://purl.org/dc/elements/1.1/\">\n")
		if m.Title != "" {
			fmt.Fprintf(&b, "   <dc:title><rdf:Alt><rdf:li xml:lang=\"x-default\">%s</rdf:li></rdf:Alt></dc:title>\n", textEsc.Replace(m.Title))
		}
		if m.Subject != "" {
			fmt.Fprintf(&b, "   <dc:description><rdf:Alt><rdf:li xml:lang=\"x-default\">%s</rdf:li></rdf:Alt></dc:description>\n", textEsc.Replace(m.Subject))
		}
		b.WriteString("  </rdf:Description>\n")
	}
	if m.Author != "" {
		b.WriteString("  <rdf:Description rdf:about=\"\" xmlns:dc=\"http://purl.org/dc/elements/1.1/\">\n")
		fmt.Fprintf(&b, "   <dc:creator><rdf:Seq><rdf:li>%s</rdf:li></rdf:Seq></dc:creator>\n", textEsc.Replace(m.Author))
		b.WriteString("  </rdf:Description>\n")
	}
	if m.Producer != "" || m.Keywords != "" {
		b.WriteString("  <rdf:Description rdf:about=\"\" xmlns:pdf=\"http://ns.adobe.com/pdf/1.3/\">\n")
		if m.Producer != "" {
			fmt.Fprintf(&b, "   <pdf:Producer>%s</pdf:Producer>\n", textEsc.Replace(m.Producer))
		}
		if m.Keywords != "" {
			fmt.Fprintf(&b, "   <pdf:Keywords>%s</pdf:Keywords>\n", textEsc.Replace(m.Keywords))
		}
		b.WriteString("  </rdf:Description>\n")
	}
	fmt.Fprintf(&b, "  <rdf:Description rdf:about=\"\" xmlns:xmp=\"http://ns.adobe.com/xap/1.0/\">\n   <xmp:CreatorTool>%s</xmp:CreatorTool>\n   <xmp:CreateDate>%s</xmp:CreateDate>\n   <xmp:ModifyDate>%s</xmp:ModifyDate>\n  </rdf:Description>\n",
		textEsc.Replace(m.CreatorTool), xmpDate(m.Created), xmpDate(m.Modified))

	// The hybrid-document identification: which XML, which profile, which
	// version. Factur-X readers read exactly these four values.
	pre := p.Prefix()
	fmt.Fprintf(&b, "  <rdf:Description rdf:about=\"\" xmlns:%s=\"%s\">\n", pre, quotEsc.Replace(p.URN()))
	fmt.Fprintf(&b, "   <%s:DocumentType>%s</%s:DocumentType>\n   <%s:DocumentFileName>%s</%s:DocumentFileName>\n   <%s:Version>%s</%s:Version>\n   <%s:ConformanceLevel>%s</%s:ConformanceLevel>\n  </rdf:Description>\n",
		pre, textEsc.Replace(p.DocType()), pre, pre, textEsc.Replace(p.Filename()), pre, pre, p.Version(), pre, pre, textEsc.Replace(p.XMPLevel()), pre)

	// Schema declaration for the properties above.
	fmt.Fprintf(&b, "  <rdf:Description rdf:about=\"\" xmlns:pdfaExtension=\"http://www.aiim.org/pdfa/ns/extension/\" xmlns:pdfaSchema=\"http://www.aiim.org/pdfa/ns/schema#\" xmlns:pdfaProperty=\"http://www.aiim.org/pdfa/ns/property#\">\n")
	b.WriteString("   <pdfaExtension:schemas><rdf:Bag><rdf:li rdf:parseType=\"Resource\">\n")
	fmt.Fprintf(&b, "    <pdfaSchema:schema>%s PDFA Extension Schema</pdfaSchema:schema>\n    <pdfaSchema:namespaceURI>%s</pdfaSchema:namespaceURI>\n    <pdfaSchema:prefix>%s</pdfaSchema:prefix>\n    <pdfaSchema:property><rdf:Seq>\n",
		textEsc.Replace(p.String()), quotEsc.Replace(p.URN()), pre)
	for _, pr := range []struct{ name, vtype, desc string }{
		{"DocumentFileName", "Text", "The name of the embedded XML document"},
		{"DocumentType", "Text", "The type of the hybrid document in capital letters, e.g. INVOICE or ORDER"},
		{"Version", "Text", "The actual version of the standard applying to the embedded XML document"},
		{"ConformanceLevel", "Text", "The conformance level of the embedded XML document"},
	} {
		fmt.Fprintf(&b, "     <rdf:li rdf:parseType=\"Resource\"><pdfaProperty:name>%s</pdfaProperty:name><pdfaProperty:valueType>%s</pdfaProperty:valueType><pdfaProperty:category>external</pdfaProperty:category><pdfaProperty:description>%s</pdfaProperty:description></rdf:li>\n",
			pr.name, pr.vtype, textEsc.Replace(pr.desc))
	}
	b.WriteString("    </rdf:Seq></pdfaSchema:property>\n   </rdf:li></rdf:Bag></pdfaExtension:schemas>\n  </rdf:Description>\n")
	b.WriteString(" </rdf:RDF>\n</x:xmpmeta>\n<?xpacket end=\"w\"?>\n")
	return []byte(b.String())
}
