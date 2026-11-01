package facturx

import (
	"crypto/md5"
	"errors"
	"fmt"
	"path"
	"sort"
	"strings"
	"time"

	"github.com/dcnws/invoicekit.go/internal/pdf"
)

// Attachment is a file carried inside the PDF alongside the invoice XML.
// Factur-X allows arbitrary attachments (supporting documents, machine-readable
// line detail, signed receipts) as long as they are declared in the same two
// places the XML is: the /EmbeddedFiles name tree and the catalogue's /AF array.
type Attachment struct {
	Filename string
	Data     []byte

	// MIME is the stream subtype, e.g. "text/xml" or "application/pdf". When
	// empty it is guessed from the file extension.
	MIME string

	// Desc is a human-readable description shown by readers.
	Desc string

	// Relationship says what the file is to the document: "Data", "Source",
	// "Alternative", "Supplement" or "Unspecified". The XML payload is Data;
	// anything else is normally Supplement.
	Relationship string
}

// Options configures Embed. The zero value is valid: the profile is read from
// the XML, the PDF/A flavour is 3b, and the metadata is filled with defaults
// and whatever the source document already carried.
type Options struct {
	// Profile declares the standard. Zero means "detect from the XML", which
	// is almost always right.
	Profile Profile

	// Conformance is the PDF/A-3 flavour: "B" (default), "U" or "A". B and U
	// differ only in what the author has to assert; A additionally requires a
	// marked-up, tagged PDF, which this package does not fabricate.
	Conformance string

	Metadata Metadata

	// Attachments are extra files to embed.
	Attachments []Attachment

	// Now fixes the timestamps written into XMP, /Info and the file
	// parameters. Zero means the current time; set it in tests, and when a
	// document must be reproducible byte for byte.
	Now time.Time

	// Tagged marks the document as tagged (/MarkInfo /Marked true) and supplies
	// an empty structure tree. Only set it when the content stream really is
	// marked up, which is what PDF/A-3A demands.
	Tagged bool
}

// Embed turns an ordinary PDF and an invoice XML into a hybrid document: the
// PDF becomes PDF/A-3 with the XML attached as factur-x.xml (or the name the
// flavour requires), plus XMP metadata, an OutputIntent with an sRGB profile,
// and file parameters a reader can verify.
//
// The PDF's own content is left alone; only the document-level structures that
// PDF/A-3 and Factur-X require are added or replaced. A PDF that is not
// already PDF/A-clean — fonts that are not embedded, transparency, encryption —
// stays not clean, because those are properties of the content. Audit reports
// what is missing; only the envelope can be fixed mechanically.
func Embed(original, xmlData []byte, opts Options) ([]byte, error) {
	if len(xmlData) == 0 {
		return nil, errors.New("facturx: no XML to embed")
	}
	prof, err := opts.resolveProfile(xmlData)
	if err != nil {
		return nil, err
	}
	conf, err := conformance(opts.Conformance)
	if err != nil {
		return nil, err
	}

	f, err := pdf.Parse(original)
	if err != nil {
		return nil, err
	}
	root, err := f.Root()
	if err != nil {
		return nil, err
	}

	// A hybrid document already carries its invoice XML. Wrapping it again would
	// leave two attachments with the same name and a reader takes whichever it
	// finds first, so the second wrap is refused: the fix belongs with the
	// original pages, not with a file that already has an invoice in it.
	if want := prof.Filename(); want != "" {
		for _, e := range embeddedFileNames(f, root) {
			if strings.EqualFold(e.name, want) {
				return nil, fmt.Errorf("facturx: this PDF already carries a %q attachment: it is a hybrid document, so wrap the invoice pages it was made from instead", want)
			}
		}
	}

	now := opts.Now
	if now.IsZero() {
		now = time.Now().UTC()
	}
	// What the source document says comes first, then the caller's values,
	// then the defaults: in that order a title written by the producer is
	// inherited, a title supplied here wins, and only the gaps get filled in.
	md := inherit(f, opts.Metadata).fill(now)

	// --- the XML payload -------------------------------------------------
	xmlSpec, err := addFile(f, Attachment{
		Filename:     prof.Filename(),
		Data:         xmlData,
		MIME:         "text/xml",
		Desc:         prof.String() + " XML file",
		Relationship: "Data",
	}, now)
	if err != nil {
		return nil, err
	}
	names := map[string]int{prof.Filename(): xmlSpec}
	for _, a := range opts.Attachments {
		if err := checkAttachment(&a, names); err != nil {
			return nil, err
		}
		num, err := addFile(f, a, now)
		if err != nil {
			return nil, err
		}
		names[a.Filename] = num
	}
	if err := writeNameTree(f, root, names); err != nil {
		return nil, err
	}

	// --- XMP -------------------------------------------------------------
	packet := xmpPacket(md, prof, conf)
	meta := pdf.NewStream(packet, pdf.Name("Type"), pdf.Name("Metadata"), pdf.Name("Subtype"), pdf.Name("XML"))
	if err := meta.Compress(-1); err != nil {
		return nil, err
	}
	root.Set("Metadata", pdf.Ref{Num: f.Add(meta)})

	// --- OutputIntent ----------------------------------------------------
	if err := ensureOutputIntent(f, root); err != nil {
		return nil, err
	}

	// --- document information dictionary ---------------------------------
	if err := writeInfo(f, md); err != nil {
		return nil, err
	}

	// --- reader behaviour and language -----------------------------------
	root.Set("PageMode", pdf.Name("UseAttachments")) // show the payload on open
	vp := root.DictValue("ViewerPreferences", f)
	if vp == nil {
		vp = pdf.NewDict()
	}
	vp.Set("DisplayDocTitle", pdf.Bool(true))
	root.Set("ViewerPreferences", vp)
	if md.Lang != "" {
		root.Set("Lang", textString(md.Lang))
	}
	if opts.Tagged {
		root.Set("MarkInfo", pdf.DictFrom(pdf.Name("Marked"), pdf.Bool(true)))
		if !root.Has("StructTreeRoot") {
			st := pdf.NewDict()
			st.Set("Type", pdf.Name("StructTreeRoot"))
			st.Set("K", pdf.Array{})
			root.Set("StructTreeRoot", pdf.Ref{Num: f.Add(st)})
		}
	}

	// PDF/A-3 is defined against PDF 1.7; a lower header version makes
	// validators complain about the constructs used above.
	if f.Version < "1.7" {
		f.Version = "1.7"
	}
	return f.Bytes()
}

// resolveProfile returns the profile to declare: the one asked for, checked
// against the XML, or detected from it. The level always has to be settled,
// because fx:ConformanceLevel is a mandatory property in the XMP packet.
func (o Options) resolveProfile(xmlData []byte) (Profile, error) {
	got, derr := DetectProfile(xmlData)
	p := o.Profile
	if p.Flavor == "" {
		if derr != nil {
			return Profile{}, derr
		}
		p.Flavor = got.Flavor
		if p.Level == "" {
			p.Level = got.Level
		}
	} else if derr != nil {
		return Profile{}, fmt.Errorf("facturx: refusing to embed XML that does not match %s: %w", p, derr)
	} else {
		if p.Flavor != got.Flavor {
			return Profile{}, fmt.Errorf("facturx: XML is %s but %s was asked for", got.Flavor, p.Flavor)
		}
		if p.Level != "" && got.Level != "" && p.Level != got.Level {
			return Profile{}, fmt.Errorf("facturx: XML declares %s (BT-24) but %s was asked for", got.Level, p.Level)
		}
	}
	if p.Level == "" {
		return Profile{}, fmt.Errorf("facturx: the XML declares no profile (BT-24), so %s cannot say which level it carries; pass Options.Profile to state it", p.Flavor)
	}
	if err := p.Validate(); err != nil {
		return Profile{}, err
	}
	return p, nil
}

func conformance(s string) (string, error) {
	switch strings.ToUpper(strings.TrimSpace(s)) {
	case "", "B":
		return "B", nil
	case "U":
		return "U", nil
	case "A":
		return "A", nil
	}
	return "", fmt.Errorf("facturx: PDF/A conformance must be B, U or A, got %q", s)
}

// checkAttachment validates and normalises an attachment in place, against the
// names already taken.
func checkAttachment(a *Attachment, names map[string]int) error {
	a.Filename = strings.TrimSpace(strings.ReplaceAll(a.Filename, "\\", "/"))
	if a.Filename == "" || strings.ContainsAny(a.Filename, "/") {
		return fmt.Errorf("facturx: attachment filename %q must be a bare name without a path", a.Filename)
	}
	for taken := range names {
		if strings.EqualFold(taken, a.Filename) {
			return fmt.Errorf("facturx: duplicate attachment name %q", a.Filename)
		}
	}
	if a.MIME == "" {
		a.MIME = mimeByExt(path.Ext(a.Filename))
	}
	if a.Relationship == "" {
		a.Relationship = "Supplement"
	}
	switch a.Relationship {
	case "Data", "Source", "Alternative", "Supplement", "Unspecified":
	default:
		return fmt.Errorf("facturx: attachment relationship must be Data, Source, Alternative, Supplement or Unspecified, got %q", a.Relationship)
	}
	return nil
}

func mimeByExt(ext string) string {
	switch strings.ToLower(ext) {
	case ".xml":
		return "text/xml"
	case ".pdf":
		return "application/pdf"
	case ".csv":
		return "text/csv"
	case ".txt":
		return "text/plain"
	case ".json":
		return "application/json"
	case ".png":
		return "image/png"
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".zip":
		return "application/zip"
	}
	return "application/octet-stream"
}

// addFile stores a file as an EmbeddedFile stream plus the file specification
// that names it, and returns the object number of the specification.
//
// /Params carries the size and an MD5 of the *uncompressed* bytes. Both are
// mandatory in PDF/A-3, and readers use the digest to prove the payload was not
// altered in transit, so they have to be computed before compression.
func addFile(f *pdf.File, a Attachment, now time.Time) (int, error) {
	sum := md5.Sum(a.Data)
	body := pdf.NewStream(a.Data)
	body.Dict.Set("Type", pdf.Name("EmbeddedFile"))
	body.Dict.Set("Subtype", pdf.Name(a.MIME))
	body.Dict.Set("Params", pdf.DictFrom(
		pdf.Name("Size"), pdf.Int(len(a.Data)),
		pdf.Name("CheckSum"), pdf.String(sum[:]),
		pdf.Name("CreationDate"), pdf.String(pdfDate(now)),
		pdf.Name("ModDate"), pdf.String(pdfDate(now)),
	))
	if err := body.Compress(-1); err != nil {
		return 0, err
	}
	fileNum := f.Add(body)
	ref := pdf.Ref{Num: fileNum}

	spec := pdf.NewDict()
	spec.Set("Type", pdf.Name("Filespec"))
	spec.Set("AFRelationship", pdf.Name(a.Relationship))
	spec.Set("F", textString(a.Filename))
	spec.Set("UF", textString(a.Filename))
	if a.Desc != "" {
		spec.Set("Desc", textString(a.Desc))
	}
	spec.Set("EF", pdf.DictFrom(pdf.Name("F"), ref, pdf.Name("UF"), ref))
	return f.Add(spec), nil
}

// textString chooses a PDF text string encoding: plain ASCII stays literal,
// anything else becomes UTF-16 with a byte order mark so it survives readers
// that assume the local code page.
func textString(s string) pdf.Object {
	for i := 0; i < len(s); i++ {
		if s[i] > 0x7f {
			return pdf.TextUTF16(s)
		}
	}
	return pdf.String(s)
}

// writeNameTree replaces the /EmbeddedFiles name tree with one holding exactly
// the given files, and rewrites the catalogue's /AF array to match.
//
// PDF/A-3 requires both: a file that is embedded but not listed in /AF is a
// validation failure, and a file listed but not embedded is worse. Existing
// entries whose names are not reused are dropped, since their objects are being
// replaced too.
func writeNameTree(f *pdf.File, root *pdf.Dict, names map[string]int) error {
	old := embeddedFileNames(f, root)
	for _, e := range old {
		if _, keep := names[e.name]; keep {
			continue
		}
		if r, ok := e.spec.(pdf.Ref); ok {
			sp, _ := f.Object(r.Num).(*pdf.Dict)
			if ef := sp.DictValue("EF", f); ef != nil {
				for _, key := range []string{"F", "UF", "DOS", "Mac", "Unix"} {
					if num := refNum(ef.Get(key)); num > 0 {
						f.Delete(num) // the EmbeddedFile stream
					}
				}
			}
			f.Delete(r.Num) // the file specification itself
		}
	}

	entries := make([]nameEntry, 0, len(names))
	for name, num := range names {
		entries = append(entries, nameEntry{name, pdf.Ref{Num: num}})
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].name < entries[j].name })

	pairs := make(pdf.Array, 0, 2*len(entries))
	af := make(pdf.Array, 0, len(entries))
	for _, e := range entries {
		pairs = append(pairs, pdf.String(e.name), e.spec)
		af = append(af, e.spec)
	}
	root.Set("Names", pdf.DictFrom(pdf.Name("EmbeddedFiles"), pdf.DictFrom(pdf.Name("Names"), pairs)))
	root.Set("AF", af)
	return nil
}

// embeddedFileNames collects the (name, filespec) pairs in the /EmbeddedFiles
// name tree, following /Kids. A flat single-node tree is written back, which is
// always legal and much easier to get right than maintaining the sort invariants
// of a split tree.
func embeddedFileNames(f *pdf.File, root *pdf.Dict) []nameEntry {
	node := root.DictValue("Names", f)
	if node == nil {
		return nil
	}
	return walkNameTree(f, node.DictValue("EmbeddedFiles", f), 0)
}

type nameEntry struct {
	name string
	spec pdf.Object
}

func walkNameTree(f *pdf.File, node *pdf.Dict, depth int) []nameEntry {
	if node == nil || depth > 32 {
		return nil
	}
	var out []nameEntry
	if kids := node.ArrayValue("Kids"); len(kids) > 0 {
		for _, k := range kids {
			r, ok := k.(pdf.Ref)
			if !ok {
				continue
			}
			out = append(out, walkNameTree(f, f.Object(r.Num).(*pdf.Dict), depth+1)...)
		}
	}
	names := node.ArrayValue("Names")
	for i := 0; i+1 < len(names); i += 2 {
		n := ""
		switch v := names[i].(type) {
		case pdf.String:
			n = decodeText(string(v))
		}
		if n == "" {
			continue
		}
		out = append(out, nameEntry{n, names[i+1]})
	}
	return out
}

// ensureOutputIntent guarantees the document carries one OutputIntent with an
// ICC profile, which is how PDF/A pins colour. If a suitable intent is already
// present it is left alone; otherwise the array is replaced with an sRGB one
// built from the profile this package generates.
func ensureOutputIntent(f *pdf.File, root *pdf.Dict) error {
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
		if d.NameValue("S") == "GTS_PDFA1" && d.StreamValue("DestOutputProfile", f) != nil {
			return nil
		}
	}
	prof := pdf.SRGBProfile()
	icc := pdf.NewStream(prof, pdf.Name("N"), pdf.Int(3), pdf.Name("Alternate"), pdf.Name("DeviceRGB"))
	iccNum := f.Add(icc)
	root.Set("OutputIntents", pdf.Array{pdf.DictFrom(
		pdf.Name("Type"), pdf.Name("OutputIntent"),
		pdf.Name("S"), pdf.Name("GTS_PDFA1"),
		pdf.Name("OutputConditionIdentifier"), pdf.String("sRGB IEC61966-2.1"),
		pdf.Name("RegistryName"), pdf.String("http://www.color.org"),
		pdf.Name("Info"), pdf.String("sRGB IEC61966-2.1"),
		pdf.Name("DestOutputProfile"), pdf.Ref{Num: iccNum},
	)})
	return nil
}

// writeInfo writes the legacy document information dictionary from the same
// values used for XMP. PDF/A allows the dictionary but requires that it agrees
// with the packet, so it is rebuilt rather than merged.
func writeInfo(f *pdf.File, md Metadata) error {
	oldRef := refNum(f.Trailer.Get("Info"))
	info := pdf.NewDict()
	set := func(k string, v string) {
		if v != "" {
			info.Set(k, textString(v))
		}
	}
	set("Title", md.Title)
	set("Author", md.Author)
	set("Subject", md.Subject)
	set("Keywords", md.Keywords)
	set("Creator", md.CreatorTool)
	set("Producer", md.Producer)
	info.Set("CreationDate", pdf.String(pdfDate(md.Created)))
	info.Set("ModDate", pdf.String(pdfDate(md.Modified)))
	if oldRef > 0 {
		f.Delete(oldRef)
	}
	f.Trailer.Set("Info", pdf.Ref{Num: f.Add(info)})
	return nil
}

// inherit takes the metadata a source document already carries as defaults, so
// that wrapping a PDF in a hybrid envelope does not quietly discard its title,
// author or dates.
func inherit(f *pdf.File, md Metadata) Metadata {
	info := f.Trailer.DictValue("Info", f)
	if info == nil {
		return md
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
	if md.CreatorTool == "" {
		md.CreatorTool = decodeText(info.StringValue("Creator"))
	}
	if md.Producer == "" {
		md.Producer = decodeText(info.StringValue("Producer"))
	}
	// A date from the source is only kept when nobody supplied one; /ModDate
	// always becomes "now", because the document is being modified by this
	// very operation.
	if md.Created.IsZero() {
		if t := pdfTime(info.StringValue("CreationDate")); !t.IsZero() {
			md.Created = t
		}
	}
	return md
}

// refNum returns the object number an indirect reference points at, or 0.
func refNum(v pdf.Object) int {
	if r, ok := v.(pdf.Ref); ok {
		return r.Num
	}
	return 0
}
