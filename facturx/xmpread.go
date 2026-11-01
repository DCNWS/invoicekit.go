package facturx

import (
	"encoding/xml"
	"fmt"
	"io"
	"strings"
)

// The namespaces an XMP packet is built out of. Anything in one of these is
// structure rather than content.
const (
	nsRDF           = "http://www.w3.org/1999/02/22-rdf-syntax-ns#"
	nsXMPPacket     = "adobe:ns:meta/"
	nsPDFaExtension = "http://www.aiim.org/pdfa/ns/extension/"
	nsPDFaSchema    = "http://www.aiim.org/pdfa/ns/schema#"
	nsPDFaID        = "http://www.aiim.org/pdfa/ns/id/"
	nsDC            = "http://purl.org/dc/elements/1.1/"
	nsPDF           = "http://ns.adobe.com/pdf/1.3/"
	nsXAP           = "http://ns.adobe.com/xap/1.0/"
)

// setter writes one packet property into the field it belongs to.
type setter func(*xmpInfo, string)

// xmpProps maps "namespace|local name" to the field a property fills. The keys
// are spelled out so a wrong namespace shows up as an unmatched property rather
// than as a title that silently came back empty.
var xmpProps = map[string]setter{
	nsPDFaID + "|part":        func(i *xmpInfo, v string) { i.Part, _ = atoi(v) },
	nsPDFaID + "|conformance": func(i *xmpInfo, v string) { i.Conf = v },
	nsDC + "|title":           func(i *xmpInfo, v string) { i.Title = v },
	nsDC + "|creator":         func(i *xmpInfo, v string) { i.Creator = v },
	nsDC + "|description":     func(i *xmpInfo, v string) { i.Description = v },
	nsPDF + "|Producer":       func(i *xmpInfo, v string) { i.Producer = v },
	nsPDF + "|Keywords":       func(i *xmpInfo, v string) { i.Keywords = v },
	nsXAP + "|CreatorTool":    func(i *xmpInfo, v string) { i.CreatorTool = v },
	nsXAP + "|CreateDate":     func(i *xmpInfo, v string) { i.Created = v },
	nsXAP + "|ModifyDate":     func(i *xmpInfo, v string) { i.Modified = v },
}

// flavourProps holds the four hybrid-document properties. They share one
// namespace per flavour, so they are matched by local name once the namespace
// is known to be one of them.
var flavourProps = map[string]setter{
	"DocumentType":     func(i *xmpInfo, v string) { i.DocType = v },
	"DocumentFileName": func(i *xmpInfo, v string) { i.FileName = v },
	"Version":          func(i *xmpInfo, v string) { i.Version = v },
	"ConformanceLevel": func(i *xmpInfo, v string) { i.Level = v },
}

// isStructural reports whether an element only holds other elements: the RDF
// wrappers, the packet wrapper, and the containers of the extension schema
// declaration. Those are descended into. Every other element is a property,
// whose text — including the text nested in rdf:Alt and rdf:Seq — is collected
// in one pass, which is what keeps a localised title from being read as three
// separate values.
func isStructural(start xml.StartElement) bool {
	switch start.Name.Space {
	case nsRDF:
		switch start.Name.Local {
		case "RDF", "Description", "Bag", "Seq", "Alt", "li":
			return true
		}
	case nsXMPPacket:
		return true // x:xmpmeta
	case nsPDFaExtension:
		return start.Name.Local == "schemas"
	case nsPDFaSchema:
		return start.Name.Local == "property"
	}
	return false
}

// xmpInfo is what this package needs out of an XMP packet. Only the properties
// that matter to a hybrid invoice are collected; anything else in the packet is
// ignored rather than rejected, because producers add their own vocabulary all
// the time.
type xmpInfo struct {
	Part        int    // pdfaid:part
	Conf        string // pdfaid:conformance ("B", "U", "A")
	DocType     string // fx:DocumentType
	FileName    string // fx:DocumentFileName
	Version     string // fx:Version
	Level       string // fx:ConformanceLevel
	Flavor      Flavour
	Title       string
	Creator     string
	Description string
	Producer    string
	Keywords    string
	CreatorTool string
	Created     string
	Modified    string
	HasPacket   bool
	HasSchema   bool // the pdfaExtension declaration is present
}

// flavourByURN maps a namespace URI in the packet back to a flavour.
func flavourByURN(ns string) Flavour {
	for fl, u := range urns {
		if ns == u {
			return fl
		}
	}
	return ""
}

// readXMP parses an XMP packet. An empty packet yields a zero value and no
// error; a malformed one is an error, since that is a real defect a validator
// will report.
func readXMP(packet []byte) (*xmpInfo, error) {
	out := &xmpInfo{}
	if len(packet) == 0 {
		return out, nil
	}
	out.HasPacket = true
	dec := xml.NewDecoder(strings.NewReader(string(packet)))
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return out, fmt.Errorf("facturx: XMP packet is not well-formed: %w", err)
		}
		start, ok := tok.(xml.StartElement)
		if !ok {
			continue
		}
		// Plumbing holds other elements, so it must be descended into: a
		// property is only visible one level below its Description wrapper.
		if isStructural(start) {
			continue
		}
		if start.Name.Local == "schema" || start.Name.Space == nsPDFaSchema {
			out.HasSchema = true
		}
		if f := flavourByURN(start.Name.Space); f != "" {
			out.Flavor = f
		}
		text, ok, err := collectText(dec, start.Name.Local)
		if err != nil {
			return out, err
		}
		if !ok {
			continue
		}
		out.setProperty(start.Name.Space, start.Name.Local, text)
	}
	return out, nil
}

// setProperty assigns a property's text to the field it belongs to.
func (i *xmpInfo) setProperty(ns, local, text string) {
	if set, ok := xmpProps[ns+"|"+local]; ok {
		set(i, text)
		return
	}
	if flavourByURN(ns) == "" {
		return
	}
	if set, ok := flavourProps[local]; ok {
		set(i, text)
	}
}

// collectText gathers the character data inside an element whose start tag has
// just been read, descending through the rdf:Alt and rdf:Seq wrappers XMP uses
// for localised and repeated values. It returns ok=false for an empty element
// with no text at all.
func collectText(dec *xml.Decoder, local string) (string, bool, error) {
	var parts []string
	depth := 0
	for {
		tok, err := dec.Token()
		if err != nil {
			if err == io.EOF {
				return strings.TrimSpace(strings.Join(parts, " ")), len(parts) > 0, nil
			}
			return "", false, fmt.Errorf("facturx: XMP property %s: %w", local, err)
		}
		switch t := tok.(type) {
		case xml.CharData:
			if s := strings.TrimSpace(string(t)); s != "" {
				parts = append(parts, s)
			}
		case xml.StartElement:
			depth++
		case xml.EndElement:
			if depth == 0 {
				return strings.TrimSpace(strings.Join(parts, " ")), len(parts) > 0, nil
			}
			depth--
		}
	}
}

func atoi(s string) (int, error) {
	n := 0
	for _, r := range s {
		if r < '0' || r > '9' {
			return 0, fmt.Errorf("facturx: %q is not a number", s)
		}
		n = n*10 + int(r-'0')
	}
	return n, nil
}
