// Package facturx writes and reads hybrid invoices: a PDF that carries its
// own XML payload as an attachment, in the Factur-X (and its siblings Order-X
// and ZUGFeRD) arrangement.
//
// A compliant hybrid document is two things at once. The PDF has to be
// PDF/A-3, which means document-level XMP metadata, an OutputIntent with an
// ICC profile, tagged-document markers and an embedded file whose parameters
// are declared. The XML has to be one of the cross-industry syntaxes, and the
// PDF has to say which one, in both the attachment name and the XMP extension
// schema. This package handles both halves: Embed turns an ordinary PDF and a
// valid invoice XML into a hybrid document, Extract recovers the XML, and
// Audit reports what is missing from a PDF that claims to be one.
package facturx

import (
	"bytes"
	"encoding/xml"
	"fmt"
	"io"
	"strings"
)

// Flavour is the standard the hybrid document follows. They share the PDF/A-3
// plumbing and differ in the XML syntax, the attachment name and the XMP
// extension namespace.
type Flavour string

const (
	// FacturX is the current standard (Factur-X / ZUGFeRD 2.x): CII or UBL.
	FacturX Flavour = "factur-x"
	// OrderX carries orders, order changes and order responses in CII.
	OrderX Flavour = "order-x"
	// ZUGFeRD is the retired 1.0 profile, CII only.
	ZUGFeRD Flavour = "zugferd"
)

// Level is the profile of information the XML is expected to carry.
type Level string

const (
	Minimum  Level = "minimum"
	BasicWL  Level = "basicwl"
	Basic    Level = "basic"
	Comfort  Level = "comfort"
	EN16931  Level = "en16931"
	Extended Level = "extended"
)

// Profile names the combination of flavour and level that a hybrid document
// declares. Every derived value (attachment name, XMP namespace, human label)
// follows from these two fields, so a Profile is safe to pass around by value.
type Profile struct {
	Flavor Flavour
	Level  Level
}

// profiles lists, for each flavour, the levels that exist and how each one is
// written in XMP.
var profiles = map[Flavour]map[Level]string{
	FacturX: {
		Minimum:  "MINIMUM",
		BasicWL:  "BASIC WL",
		Basic:    "BASIC",
		EN16931:  "EN 16931",
		Extended: "EXTENDED",
	},
	OrderX: {
		Basic:    "BASIC",
		Comfort:  "COMFORT",
		Extended: "EXTENDED",
	},
	ZUGFeRD: {
		Basic:    "BASIC",
		Comfort:  "COMFORT",
		Extended: "EXTENDED",
	},
}

// filenames maps a flavour to the attachment name the standard requires.
// Readers look for exactly these names, so getting them wrong hides the XML.
var filenames = map[Flavour]string{
	FacturX: "factur-x.xml",
	OrderX:  "order-x.xml",
	ZUGFeRD: "ZUGFeRD-invoice.xml",
}

// urns and prefixes give the XMP extension schema for each flavour. The
// trailing '#' is part of the namespace, not punctuation.
var urns = map[Flavour]string{
	FacturX: "urn:factur-x:pdfa:CrossIndustryDocument:invoice:1p0#",
	OrderX:  "urn:factur-x:pdfa:CrossIndustryDocument:1p0#",
	ZUGFeRD: "urn:ferd:pdfa:CrossIndustryDocument:invoice:1p0#",
}

var prefixes = map[Flavour]string{
	FacturX: "fx",
	OrderX:  "fx",
	ZUGFeRD: "zf",
}

// doctypes is the default fx:DocumentType value per flavour. Order-X documents
// may be orders, order changes or order responses; OrderType sets that.
var doctypes = map[Flavour]string{
	FacturX: "INVOICE",
	OrderX:  "ORDER",
	ZUGFeRD: "INVOICE",
}

// Validate reports whether this flavour and level combination exists.
func (p Profile) Validate() error {
	levels, ok := profiles[p.Flavor]
	if !ok {
		return fmt.Errorf("facturx: unknown flavour %q (want factur-x, order-x or zugferd)", p.Flavor)
	}
	if _, ok := levels[p.Level]; !ok {
		want := make([]string, 0, len(levels))
		for _, l := range LevelOrder(p.Flavor) {
			want = append(want, string(l))
		}
		return fmt.Errorf("facturx: flavour %s has no level %q (want one of %s)", p.Flavor, p.Level, strings.Join(want, ", "))
	}
	return nil
}

// Filename returns the attachment name the XML must be stored under.
func (p Profile) Filename() string { return filenames[p.Flavor] }

// URN returns the XMP extension namespace for the flavour.
func (p Profile) URN() string { return urns[p.Flavor] }

// Prefix returns the XMP namespace prefix conventionally used for that URN.
func (p Profile) Prefix() string { return prefixes[p.Flavor] }

// XMPLevel returns the fx:ConformanceLevel value for the profile.
func (p Profile) XMPLevel() string { return profiles[p.Flavor][p.Level] }

// DocType returns the fx:DocumentType value for the profile.
func (p Profile) DocType() string { return doctypes[p.Flavor] }

// Version returns the fx:Version value written into XMP. All three flavours
// carry a document syntax at version 1.0 inside the PDF/A-3 envelope.
func (p Profile) Version() string { return "1.0" }

// String renders the profile the way the standard's own documentation writes
// it, for example "Factur-X EN 16931" or "Order-X Comfort".
func (p Profile) String() string {
	name := map[Flavour]string{FacturX: "Factur-X", OrderX: "Order-X", ZUGFeRD: "ZUGFeRD 1.0"}[p.Flavor]
	if name == "" {
		return string(p.Flavor) + "/" + string(p.Level)
	}
	lvl := string(p.Level)
	if lvl != "" {
		lvl = " " + title(lvl)
		if p.Flavor == FacturX && p.Level == BasicWL {
			lvl = " BASIC WL"
		}
		if p.Flavor == FacturX && p.Level == EN16931 {
			lvl = " EN 16931"
		}
	}
	return name + lvl
}

func title(s string) string {
	r := []rune(s)
	if len(r) == 0 {
		return s
	}
	return strings.ToUpper(string(r[0])) + string(r[1:])
}

// LevelOrder lists the levels of a flavour in ascending order of information.
func LevelOrder(f Flavour) []Level {
	switch f {
	case FacturX:
		return []Level{Minimum, BasicWL, Basic, EN16931, Extended}
	case OrderX, ZUGFeRD:
		return []Level{Basic, Comfort, Extended}
	}
	return nil
}

// Profiles lists every valid flavour and level combination.
func Profiles() []Profile {
	var out []Profile
	for _, f := range []Flavour{FacturX, OrderX, ZUGFeRD} {
		for _, l := range LevelOrder(f) {
			out = append(out, Profile{Flavor: f, Level: l})
		}
	}
	return out
}

// flavourAliases spells the flavours the way people type them. The order
// matters: "zugferd 2" is Factur-X, so it has to be tried before "zugferd".
var flavourAliases = []struct {
	spelling string
	flavour  Flavour
}{
	{"factur-x", FacturX},
	{"factur x", FacturX},
	{"facturx", FacturX},
	{"zugferd 2", FacturX},
	{"zf 2", FacturX},
	{"zugferd", ZUGFeRD},
	{"zf", ZUGFeRD},
	{"order-x", OrderX},
	{"order x", OrderX},
	{"orderx", OrderX},
}

// splitFlavour peels a leading flavour spelling off a profile string.
func splitFlavour(t string) (Flavour, string, bool) {
	for _, a := range flavourAliases {
		if rest, ok := strings.CutPrefix(t, a.spelling); ok {
			return a.flavour, strings.TrimSpace(rest), true
		}
	}
	return "", "", false
}

// normaliseFlavour accepts the spellings that arrive on the left of a slash.
func normaliseFlavour(f Flavour) Flavour {
	switch strings.ReplaceAll(strings.ToLower(string(f)), "_", " ") {
	case "facturx", "factur x", "zugferd2", "zugferd 2", "zugferd 2.0", "zugferd 2.x":
		return FacturX
	case "orderx", "order x":
		return OrderX
	}
	return f
}

// ParseProfile accepts a profile given as "en16931", "factur-x/en16931",
// "Factur-X EN 16931" or "ZUGFeRD comfort". A bare level is read as Factur-X,
// which is what people mean when they say "extended".
func ParseProfile(s string) (Profile, error) {
	t := strings.ToLower(strings.TrimSpace(s))
	t = strings.ReplaceAll(t, "_", " ")
	if t == "basic wl" || t == "basic-wl" || t == "basicwl" {
		return Profile{Flavor: FacturX, Level: BasicWL}, nil
	}
	var f Flavour
	lvl := ""
	if i := strings.IndexByte(t, '/'); i >= 0 {
		f, lvl = normaliseFlavour(Flavour(strings.TrimSpace(t[:i]))), strings.TrimSpace(t[i+1:])
	} else if alias, rest, ok := splitFlavour(t); ok {
		f, lvl = alias, rest
	} else {
		// No flavour at all: "extended" means Factur-X extended in ordinary
		// speech, and Validate rejects a level that flavour does not have.
		f, lvl = FacturX, t
	}
	// Aliases are resolved before validation, so that a level the flavour does
	// not have is rejected here rather than reaching a writer three layers on.
	lvl = strings.TrimSpace(strings.ReplaceAll(lvl, "-", " "))
	var level Level
	switch lvl {
	case "basic wl":
		level = BasicWL
	case "en 16931", "en16931", "cen 16931":
		level = EN16931
	case "minimum":
		level = Minimum
	default:
		level = Level(lvl)
	}
	p := Profile{Flavor: f, Level: level}
	if err := p.Validate(); err != nil {
		return Profile{}, err
	}
	return p, nil
}

// guidelineIDs maps the context parameter written inside the XML (BT-24 for
// invoices) to the profile it declares. The mapping is the one published with
// the standards; #compliant# and #conformant# are both seen in the wild.
var guidelineIDs = map[string]Profile{
	"urn:factur-x.eu:1p0:minimum":                                     {FacturX, Minimum},
	"urn:factur-x.eu:1p0:basicwl":                                     {FacturX, BasicWL},
	"urn:cen.eu:en16931:2017#compliant#urn:factur-x.eu:1p0:basicwl":   {FacturX, BasicWL},
	"urn:cen.eu:en16931:2017#compliant#urn:factur-x.eu:1p0:basic":     {FacturX, Basic},
	"urn:cen.eu:en16931:2017":                                         {FacturX, EN16931},
	"urn:cen.eu:en16931:2017#conformant#urn:factur-x.eu:1p0:extended": {FacturX, Extended},
	"urn:cen.eu:en16931:2017#compliant#urn:factur-x.eu:1p0:extended":  {FacturX, Extended},
	"urn:order-x.eu:1p0:basic":                                        {OrderX, Basic},
	"urn:order-x.eu:1p0:comfort":                                      {OrderX, Comfort},
	"urn:order-x.eu:1p0:extended":                                     {OrderX, Extended},
	"urn:ferd:CrossIndustryDocument:invoice:1p0:basic":                {ZUGFeRD, Basic},
	"urn:ferd:CrossIndustryDocument:invoice:1p0:comfort":              {ZUGFeRD, Comfort},
	"urn:ferd:CrossIndustryDocument:invoice:1p0:extended":             {ZUGFeRD, Extended},
}

// syntaxes maps a root element to its flavour, used when the XML carries no
// usable context parameter.
var syntaxes = map[string]Flavour{
	"urn:un:unece:uncefact:data:standard:CrossIndustryInvoice:100": FacturX,
	"urn:un:unece:uncefact:data:SCRDMCCBDACIOMessageStructure:100": OrderX,
	"urn:ferd:CrossIndustryDocument:invoice:1p0":                   ZUGFeRD,
	"urn:oasis:names:specification:ubl:schema:xsd:Invoice-2":       FacturX,
	"urn:oasis:names:specification:ubl:schema:xsd:CreditNote-2":    FacturX,
}

// DetectProfile reads the XML and reports the profile it declares. The
// authoritative marker is the context parameter: the guideline identifier for
// CII (BT-24), the profile or customisation identifier for UBL. When the
// document does not state one, the root element still fixes the flavour, and
// the level comes back empty.
//
// The returned error is non-nil only when the bytes are not a cross-industry
// document at all.
func DetectProfile(data []byte) (Profile, error) {
	dec := xml.NewDecoder(bytes.NewReader(data))
	var (
		root, rootNS     string
		guideline, other string
		stack            []string
	)
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return Profile{}, fmt.Errorf("facturx: XML is not well-formed: %w", err)
		}
		switch t := tok.(type) {
		case xml.StartElement:
			if root == "" {
				root, rootNS = t.Name.Local, t.Name.Space
			}
			stack = append(stack, t.Name.Local)
			parent := ""
			if len(stack) >= 2 {
				parent = stack[len(stack)-2]
			}
			kind, isID := contextID(t.Name.Local, parent)
			if isID {
				txt, err := elemText(dec)
				if err == nil {
					txt = strings.TrimSpace(txt)
					if strings.HasPrefix(txt, "urn:") {
						if kind == strongID && guideline == "" {
							guideline = txt
						} else if kind == weakID && other == "" {
							other = txt
						}
					}
					stack = stack[:len(stack)-1] // elemText consumed the close tag
				}
			}
		case xml.EndElement:
			if len(stack) > 0 {
				stack = stack[:len(stack)-1]
			}
		}
		if guideline != "" {
			break
		}
	}
	if root == "" {
		return Profile{}, fmt.Errorf("facturx: no XML document to read")
	}
	f, ok := syntaxes[rootNS]
	if !ok {
		return Profile{}, fmt.Errorf("facturx: %s{%s} is not a syntax this package writes", rootNS, root)
	}
	if guideline == "" {
		guideline = other
	}
	if guideline != "" {
		if p, ok := resolveProfile(guideline); ok {
			return p, nil
		}
	}
	return Profile{Flavor: f}, nil
}

// idKind says how much weight an element's text carries as a profile marker.
type idKind int

const (
	weakID idKind = iota
	strongID
)

// contextID classifies an element that may hold a profile identifier. The
// authoritative marker is the identifier inside
// GuidelineSpecifiedDocumentContextParameter (BT-24) for CII, and ProfileID or
// CustomizationID for UBL. Other context parameters exist too -- the business
// process in particular is written as a URN by some senders -- so they count
// only as a fallback.
func contextID(name, parent string) (idKind, bool) {
	switch {
	case name == "ProfileID" || name == "CustomizationID" ||
		(name == "ID" && strings.Contains(parent, "Guideline")):
		return strongID, true
	case name == "ID" && strings.Contains(parent, "ContextParameter"):
		return weakID, true
	}
	return 0, false
}

// resolveProfile maps an identifier URN to a profile. National and sector
// customisations extend a base identifier with a # suffix; the part in front of
// it still fixes the base profile.
func resolveProfile(id string) (Profile, bool) {
	if p, ok := guidelineIDs[id]; ok {
		return p, true
	}
	p, ok := guidelineIDs[strings.SplitN(id, "#", 2)[0]]
	return p, ok
}

// elemText reads the character data of the element whose start tag has just
// been consumed, skipping any nested elements, and leaves the decoder after
// the matching end tag.
func elemText(dec *xml.Decoder) (string, error) {
	var b strings.Builder
	for {
		tok, err := dec.Token()
		if err != nil {
			return "", err
		}
		switch t := tok.(type) {
		case xml.CharData:
			b.WriteString(string(t))
		case xml.StartElement:
			if err := dec.Skip(); err != nil {
				return "", err
			}
		case xml.EndElement:
			return strings.TrimSpace(b.String()), nil
		}
	}
}
