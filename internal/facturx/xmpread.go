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
)

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
		switch start.Name.Space + "|" + start.Name.Local {
		case "http://www.aiim.org/pdfa/ns/id/|part":
			out.Part, _ = atoi(text)
		case "http://www.aiim.org/pdfa/ns/id/|conformance":
			out.Conf = text
		case "http://purl.org/dc/elements/1.1/|title":
			out.Title = text
		case "http://purl.org/dc/elements/1.1/|creator":
			out.Creator = text
		case "http://purl.org/dc/elements/1.1/|description":
			out.Description = text
		case "http://ns.adobe.com/pdf/1.3/|Producer":
			out.Producer = text
		case "http://ns.adobe.com/pdf/1.3/|Keywords":
			out.Keywords = text
		case "http://ns.adobe.com/xap/1.0/|CreatorTool":
			out.CreatorTool = text
		case "http://ns.adobe.com/xap/1.0/|CreateDate":
			out.Created = text
		case "http://ns.adobe.com/xap/1.0/|ModifyDate":
			out.Modified = text
		default:
			// The four hybrid-document properties share one namespace per
			// flavour, so match on the local name once the namespace is known
			// to be one of them.
			if flavourByURN(start.Name.Space) != "" {
				switch start.Name.Local {
				case "DocumentType":
					out.DocType = text
				case "DocumentFileName":
					out.FileName = text
				case "Version":
					out.Version = text
				case "ConformanceLevel":
					out.Level = text
				}
			}
		}
	}
	return out, nil
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
