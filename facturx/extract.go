package facturx

import (
	"bytes"
	"crypto/md5"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/dcnws/invoicekit.go/internal/pdf"
)

// FileEntry describes one file carried inside a hybrid document.
type FileEntry struct {
	Filename     string
	MIME         string
	Relationship string // Data, Supplement, Source, Alternative or Unspecified
	Size         int    // uncompressed, as declared in /Params
	RecordedMD5  []byte // digest declared in /Params, nil when absent
	DigestOK     bool   // the stored bytes match that digest
	IsXML        bool   // true for the invoice payload itself
}

// Document is everything a hybrid PDF declares and carries.
type Document struct {
	// Profile is the flavour and level as the PDF declares them: read from the
	// XMP extension when present, otherwise from the XML.
	Profile Profile

	// Declared is the profile stated inside the XML itself (BT-24 for CII,
	// ProfileID for UBL). Comparing it with Profile is how a mismatch between
	// the envelope and the payload is caught.
	Declared Profile

	// XMPLevel is fx:ConformanceLevel verbatim, empty when absent.
	XMPLevel string

	// XMLFilename is the attachment name the payload was found under.
	XMLFilename string

	// XML is the decompressed payload, empty when there is none.
	XML []byte

	// Metadata holds the title, author and dates, from XMP first and from the
	// legacy document information dictionary as a fallback.
	Metadata Metadata

	// XMP keeps the packet properties this package understands.
	XMP xmpInfo

	// PDFVersion is the header version of the file.
	PDFVersion string

	// Files lists every embedded file, the XML payload included.
	Files []FileEntry

	// XMLAttachments lists every attachment whose name or bytes say XML, the
	// payload included. More than one means the recipient cannot tell which
	// invoice the pages belong to, which is what the audit reports.
	XMLAttachments []string

	// Pages is the page count, 0 when the page tree could not be walked.
	Pages int
}

// Extract returns the XML payload of a hybrid PDF and the profile it carries.
// It is the common case of Inspect, for callers who only want the invoice.
func Extract(data []byte) ([]byte, Profile, error) {
	doc, err := Inspect(data)
	if err != nil {
		return nil, Profile{}, err
	}
	if len(doc.XML) == 0 {
		return nil, Profile{}, errors.New("facturx: this PDF carries no invoice XML")
	}
	return doc.XML, doc.Profile, nil
}

// Inspect reads a hybrid PDF: the payload, what the document claims about it,
// and the file inventory. It does not judge conformance; Audit does that, and
// the two answer different questions. An ordinary PDF is not an error here, it
// simply comes back with no payload.
func Inspect(data []byte) (*Document, error) {
	f, err := pdf.Parse(data)
	if err != nil {
		return nil, err
	}
	root, err := f.Root()
	if err != nil {
		return nil, err
	}
	doc := &Document{PDFVersion: f.Version, Pages: countPages(f, root)}

	xmp := xmpStream(f, root)
	info, err := readXMP(xmp)
	if err != nil {
		return nil, err
	}
	doc.XMP = *info
	if info.Flavor != "" && info.Level != "" {
		if p, err := ParseProfile(string(info.Flavor) + "/" + info.Level); err == nil {
			doc.Profile = p
		}
	}
	doc.XMLFilename = info.FileName

	for _, e := range embeddedFileNames(f, root) {
		spec, ok := f.Object(refNum(e.spec)).(*pdf.Dict)
		if !ok {
			continue
		}
		if err := doc.addEmbeddedFile(f, spec, e.name); err != nil {
			return nil, err
		}
	}

	if len(doc.XML) > 0 {
		if p, err := DetectProfile(doc.XML); err == nil {
			doc.Declared = p
			if doc.Profile.Flavor == "" {
				doc.Profile = p
			}
		}
	}
	fillMetadata(f, doc)
	sort.Slice(doc.Files, func(i, j int) bool { return doc.Files[i].Filename < doc.Files[j].Filename })
	return doc, nil
}

// addEmbeddedFile reads one file specification into the inventory and takes it
// as the invoice payload when it looks like one. A specification with no
// embedded stream is skipped rather than reported: hybrid PDFs routinely carry
// /AF entries that point at nothing.
func (d *Document) addEmbeddedFile(f *pdf.File, spec *pdf.Dict, fallback string) error {
	name := fallback
	if n := decodeText(spec.StringValue("UF")); n != "" {
		name = n
	}
	fe := FileEntry{Filename: name, Relationship: spec.NameValue("AFRelationship")}
	// A file specification's /EF holds streams, not dictionaries, so
	// StreamValue (which resolves the reference) is the only accessor that
	// works here. /UF is the Unicode name's stream and usually identical.
	ef := spec.DictValue("EF", f)
	if ef == nil {
		return nil
	}
	s := ef.StreamValue("F", f)
	if s == nil {
		s = ef.StreamValue("UF", f)
	}
	if s == nil {
		return nil
	}
	fe.MIME = s.Dict.NameValue("Subtype")
	readEFParams(&fe, s.Dict.DictValue("Params", f))
	body, err := f.StreamBytes(s)
	if err != nil {
		return fmt.Errorf("facturx: cannot decode embedded file %q: %w", name, err)
	}
	if len(fe.RecordedMD5) > 0 {
		sum := md5.Sum(body)
		fe.DigestOK = bytes.Equal(sum[:], fe.RecordedMD5)
	}
	if isXMLBody(fe.MIME, body) {
		d.XMLAttachments = append(d.XMLAttachments, name)
	}
	if len(d.XML) == 0 && isPayload(name, d.XMLFilename) {
		d.XML = body
		d.XMLFilename = name
		fe.IsXML = true
	}
	d.Files = append(d.Files, fe)
	return nil
}

// readEFParams copies the size and checksum a file stream records under /Params.
func readEFParams(fe *FileEntry, params *pdf.Dict) {
	if params == nil {
		return
	}
	if n, ok := params.IntValue("Size"); ok {
		fe.Size = int(n)
	}
	if cs, ok := params.Get("CheckSum").(pdf.String); ok {
		fe.RecordedMD5 = []byte(cs)
	}
}

// payloadEntry returns the inventory entry of the attachment that was taken as
// the invoice, or nil when the payload came from somewhere else.
func (d *Document) payloadEntry() *FileEntry {
	for i := range d.Files {
		if d.Files[i].IsXML {
			return &d.Files[i]
		}
	}
	return nil
}

// xmlMIMETypes are the media types producers give the invoice attachment.
var xmlMIMETypes = map[string]bool{"text/xml": true, "application/xml": true}

// isXMLBody reports whether an attachment is XML, judged either by its declared
// media type or by the declaration that opens it.
func isXMLBody(mime string, body []byte) bool {
	return xmlMIMETypes[mime] || bytes.HasPrefix(bytes.TrimSpace(body), []byte("<?xml"))
}

// fillMetadata merges the descriptive fields, preferring XMP because PDF/A
// treats the packet as authoritative, and falling back to the information
// dictionary for documents that predate it.
func fillMetadata(f *pdf.File, doc *Document) {
	md := Metadata{
		Title:       doc.XMP.Title,
		Author:      doc.XMP.Creator,
		Subject:     doc.XMP.Description,
		Keywords:    doc.XMP.Keywords,
		Producer:    doc.XMP.Producer,
		CreatorTool: doc.XMP.CreatorTool,
		Created:     timeText(doc.XMP.Created),
		Modified:    timeText(doc.XMP.Modified),
	}
	info := f.Trailer.DictValue("Info", f)
	if info == nil {
		doc.Metadata = md
		return
	}
	if md.Title == "" {
		md.Title = decodeText(info.StringValue("Title"))
	}
	if md.Author == "" {
		md.Author = decodeText(info.StringValue("Author"))
	}
	if md.Subject == "" {
		md.Subject = decodeText(info.StringValue("Subject"))
	}
	if md.Keywords == "" {
		md.Keywords = decodeText(info.StringValue("Keywords"))
	}
	if md.Producer == "" {
		md.Producer = decodeText(info.StringValue("Producer"))
	}
	if md.CreatorTool == "" {
		md.CreatorTool = decodeText(info.StringValue("Creator"))
	}
	if md.Created.IsZero() {
		md.Created = pdfTime(info.StringValue("CreationDate"))
	}
	if md.Modified.IsZero() {
		md.Modified = pdfTime(info.StringValue("ModDate"))
	}
	doc.Metadata = md
}

func timeText(s string) time.Time {
	if s == "" {
		return time.Time{}
	}
	for _, layout := range []string{time.RFC3339Nano, "2006-01-02T15:04:05", "2006-01-02"} {
		if t, err := time.Parse(layout, s); err == nil {
			return t
		}
	}
	return time.Time{}
}

// isPayload decides whether an attachment is the invoice itself. The name the
// profile requires wins; failing that, any XML attachment is treated as a
// candidate, because some producers use their own file name.
func isPayload(name, wanted string) bool {
	if wanted != "" && strings.EqualFold(name, wanted) {
		return true
	}
	for _, known := range filenames {
		if strings.EqualFold(name, known) {
			return true
		}
	}
	return strings.HasSuffix(strings.ToLower(name), ".xml")
}

// xmpStream returns the raw XMP packet, or nil when the document has none.
func xmpStream(f *pdf.File, root *pdf.Dict) []byte {
	s := root.StreamValue("Metadata", f)
	if s == nil {
		return nil
	}
	if sub := s.Dict.NameValue("Subtype"); sub != "" && sub != "XML" {
		return nil
	}
	b, err := f.StreamBytes(s)
	if err != nil {
		return nil
	}
	return b
}

// countPages returns the page count. It is the cheapest signal that the file
// really is an invoice rendering rather than an empty envelope.
func countPages(f *pdf.File, root *pdf.Dict) int {
	node := root.DictValue("Pages", f)
	if node == nil {
		return 0
	}
	if n, ok := node.IntValue("Count"); ok {
		return int(n)
	}
	return countKids(f, node, 0)
}

func countKids(f *pdf.File, node *pdf.Dict, depth int) int {
	if depth > 32 {
		return 0
	}
	n := 0
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
			n++
			continue
		}
		n += countKids(f, child, depth+1)
	}
	return n
}
