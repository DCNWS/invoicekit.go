package en16931

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/dcnws/invoicekit.go/internal/xp"
)

// TestSchemaCorpora runs the official EN 16931 instances through the schema
// checker. Every one of them is a valid document in the eyes of the CEF
// validators, so a schema finding here is a false positive in our checker - the
// same argument as TestFullDocuments makes for the Schematron rules.
func TestSchemaCorpora(t *testing.T) {
	root := refDir(t)
	dirs := []string{
		"test/testfiles",
		"cii/examples",
		"ubl/examples",
	}
	var docs, bad int
	for _, d := range dirs {
		for _, f := range glob(t, filepath.Join(root, d), "*.xml") {
			data := read(t, f)
			rep, err := Validate(data, Options{})
			if err != nil {
				t.Errorf("%s: %v", f, err)
				continue
			}
			docs++
			if len(rep.Schema) > 0 {
				bad++
				t.Errorf("%s [%s]: %d schema findings", f, rep.Syntax, len(rep.Schema))
				for i, fi := range rep.Schema {
					if i < 5 {
						t.Logf("   %v", fi)
					}
				}
			}
		}
	}
	if docs < 55 {
		t.Fatalf("only %d documents checked; the corpus is not where it should be", docs)
	}
	t.Logf("%d official documents, %d with schema findings", docs, bad)
}

// TestSchemaModels checks the embedded models themselves: they must parse, they
// must be cached, and they must know the two root elements and nothing else.
func TestSchemaModels(t *testing.T) {
	for _, tc := range []struct {
		sx   Syntax
		root string
	}{
		{SyntaxCII, "{urn:un:unece:uncefact:data:standard:CrossIndustryInvoice:100}CrossIndustryInvoice"},
		{SyntaxUBL, "{urn:oasis:names:specification:ubl:schema:xsd:Invoice-2}Invoice"},
		{SyntaxUBL, "{urn:oasis:names:specification:ubl:schema:xsd:CreditNote-2}CreditNote"},
	} {
		m, err := SchemaModel(tc.sx)
		if err != nil {
			t.Fatalf("%s: %v", tc.sx, err)
		}
		if _, ok := m.Decls[tc.root]; !ok {
			t.Errorf("%s model does not declare %s", tc.sx, tc.root)
		}
	}
	m1, err := SchemaModel(SyntaxUBL)
	if err != nil {
		t.Fatal(err)
	}
	m2, err := SchemaModel(SyntaxUBL)
	if err != nil {
		t.Fatal(err)
	}
	if m1 != m2 {
		t.Error("SchemaModel is not cached; a model was rebuilt")
	}
}

// TestSchemaAgainstWrongSyntax checks that a document of one syntax is reported
// as unknown to the other syntax rather than being half checked.
func TestSchemaAgainstWrongSyntax(t *testing.T) {
	root := refDir(t)
	data := read(t, filepath.Join(root, "cii/examples/CII_example1.xml"))
	doc, err := xp.ParseXML(string(data))
	if err != nil {
		t.Fatal(err)
	}
	fs, err := SchemaFindings(SyntaxUBL, doc)
	if err != nil {
		t.Fatal(err)
	}
	if len(fs) != 1 || !strings.Contains(fs[0].Message, "root element") {
		t.Errorf("want one root-element finding, got %v", fs)
	}
}

// schemaMutation is a deliberate structural defect applied to an official
// instance. want is a substring of the finding that has to fire; rulesClean
// records that the official rule set cannot see this defect at all, which is
// the whole reason the schema checker exists.
type schemaMutation struct {
	name       string
	base       string
	mutate     func(t *testing.T, src string) string
	want       string
	rulesClean bool
}

// move takes the element at `from` and puts it in front of `after`, both of
// which must exist exactly once. It models the common real-world defect: the
// right elements in the wrong order.
func move(t *testing.T, src, from, after string) string {
	t.Helper()
	if strings.Count(src, from) != 1 {
		t.Fatalf("%q appears %d times, want once", from, strings.Count(src, from))
	}
	if strings.Count(src, after) != 1 {
		t.Fatalf("%q appears %d times, want once", after, strings.Count(src, after))
	}
	i, j := strings.Index(src, from), strings.Index(src, after)
	if i < j {
		t.Fatalf("%q already precedes %q", from, after)
	}
	src = src[:i] + src[i+len(from):]
	j = strings.Index(src, after)
	return src[:j] + from + src[j:]
}

func mustOnce(t *testing.T, src, old string) {
	t.Helper()
	if strings.Count(src, old) != 1 {
		t.Fatalf("%q appears %d times, want once", old, strings.Count(src, old))
	}
}

// firstBlock cuts src at the first element whose opening tag is openTag,
// returning the text before it, the element itself and the text after it. None
// of the blocks used here nest an element of the same name, so the first
// closing tag is the right one.
func firstBlock(t *testing.T, src, openTag string) (before, el, after string) {
	t.Helper()
	i := strings.Index(src, openTag)
	if i < 0 {
		t.Fatalf("%q not found in the document", openTag)
	}
	name := openTag[1 : len(openTag)-1]
	end := strings.Index(src[i:], "</"+name+">")
	if end < 0 {
		t.Fatalf("%s is never closed", openTag)
	}
	j := i + end + len("</"+name+">")
	return src[:i], src[i:j], src[j:]
}

func TestSchemaMutations(t *testing.T) {
	const (
		ublMin = "test/testfiles/Invoice-Min_content_with_VAT.xml"
		ublMax = "test/testfiles/Invoice-Max_content.xml"
		ciiEx1 = "cii/examples/CII_example1.xml"
	)
	payable := "<cbc:PayableAmount currencyID=\"SEK\">500</cbc:PayableAmount>"
	taxExcl := "<cbc:TaxExclusiveAmount currencyID=\"SEK\">400</cbc:TaxExclusiveAmount>"
	cases := []schemaMutation{
		{
			name: "ubl: amount order in LegalMonetaryTotal",
			base: ublMin,
			mutate: func(t *testing.T, s string) string {
				return move(t, s, payable, taxExcl)
			},
			want:       "element TaxExclusiveAmount is not allowed here",
			rulesClean: true,
		},
		{
			name: "ubl: Amount and BaseAmount in AllowanceCharge",
			base: ublMax,
			mutate: func(t *testing.T, s string) string {
				// The same pair appears in several allowances, so only the
				// first block is touched.
				amt := "<cbc:Amount currencyID=\"SEK\">0</cbc:Amount>"
				bas := "<cbc:BaseAmount currencyID=\"SEK\">0</cbc:BaseAmount>"
				pre, blk, post := firstBlock(t, s, "<cac:AllowanceCharge>")
				return pre + move(t, blk, bas, amt) + post
			},
			want:       "element BaseAmount is not allowed here; expected Amount",
			rulesClean: true,
		},
		{
			name: "ubl: element repeated past maxOccurs",
			base: ublMin,
			mutate: func(t *testing.T, s string) string {
				mustOnce(t, s, payable)
				return strings.Replace(s, payable, payable+payable, 1)
			},
			want: "element PayableAmount is not allowed here",
		},
		{
			// IssueDate is one of the few mandatory children of Invoice; the
			// amounts in LegalMonetaryTotal are all optional in UBL 2.1, so
			// dropping one of those is valid XML and not our business.
			name: "ubl: issue date dropped",
			base: ublMin,
			mutate: func(t *testing.T, s string) string {
				_, el, _ := firstBlock(t, s, "<cbc:IssueDate>")
				return strings.Replace(s, el, "", 1)
			},
			want: "IssueDate",
		},
		{
			name: "ubl: element name misspelt",
			base: ublMin,
			mutate: func(t *testing.T, s string) string {
				mustOnce(t, s, taxExcl)
				return strings.Replace(s, taxExcl,
					"<cbc:TaxExcludedAmount currencyID=\"SEK\">400</cbc:TaxExcludedAmount>", 1)
			},
			want: "element TaxExcludedAmount is not allowed here",
		},
		{
			name: "ubl: elements where text belongs",
			base: ublMin,
			mutate: func(t *testing.T, s string) string {
				mustOnce(t, s, payable)
				return strings.Replace(s, payable,
					"<cbc:PayableAmount currencyID=\"SEK\"><cbc:Amount>500</cbc:Amount></cbc:PayableAmount>", 1)
			},
			want: "takes a text value",
		},
		{
			// InvoiceLine is minOccurs=1, maxOccurs=unbounded, which is the
			// case an earlier version of the matcher got wrong: an unbounded
			// part with a minimum was wired as if the minimum were zero, so a
			// document with no lines at all looked well formed.
			name: "ubl: an invoice with no lines",
			base: ublMin,
			mutate: func(t *testing.T, s string) string {
				_, el, _ := firstBlock(t, s, "<cac:InvoiceLine>")
				return strings.Replace(s, el, "", 1)
			},
			want: "InvoiceLine",
		},
		{
			name: "cii: amount order in the monetary summation",
			base: ciiEx1,
			mutate: func(t *testing.T, s string) string {
				tot := "<ram:TaxTotalAmount currencyID=\"EUR\">20.73</ram:TaxTotalAmount>"
				grand := "<ram:GrandTotalAmount>250.33</ram:GrandTotalAmount>"
				return move(t, s, grand, tot)
			},
			// The D16B schema lets every amount in that block repeat, so the
			// complaint lands on the amount that has fallen behind.
			want:       "element TaxTotalAmount is not allowed here",
			rulesClean: true,
		},
		{
			// ApplicableHeaderTradeDelivery is declared without minOccurs or
			// maxOccurs, so exactly one is required and one only.
			name: "cii: header delivery component repeated",
			base: ciiEx1,
			mutate: func(t *testing.T, s string) string {
				del := "<ram:ApplicableHeaderTradeDelivery/>"
				mustOnce(t, s, del)
				return strings.Replace(s, del, del+del, 1)
			},
			want: "ApplicableHeaderTradeDelivery",
		},
		{
			name: "cii: header delivery component dropped",
			base: ciiEx1,
			mutate: func(t *testing.T, s string) string {
				del := "<ram:ApplicableHeaderTradeDelivery/>"
				mustOnce(t, s, del)
				return strings.Replace(s, del, "", 1)
			},
			want: "ApplicableHeaderTradeDelivery",
		},
		{
			// Order in a postal address: PostcodeCode comes before CityName,
			// and swapping them changes nothing the business rules can see.
			name: "cii: address element order",
			base: ciiEx1,
			mutate: func(t *testing.T, s string) string {
				return move(t, s,
					"<ram:CityName>HEEMSKERK</ram:CityName>",
					"<ram:PostcodeCode>1960 AJ</ram:PostcodeCode>")
			},
			// CityName on its own is fine - the address lines are optional -
			// but PostcodeCode may not follow it.
			want:       "element PostcodeCode is not allowed here",
			rulesClean: true,
		},
	}
	root := refDir(t)
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			src := string(read(t, filepath.Join(root, tc.base)))
			base, err := Validate([]byte(src), Options{})
			if err != nil {
				t.Fatal(err)
			}
			if len(base.Schema) != 0 {
				t.Fatalf("the unmutated document already fails: %v", base.Schema)
			}
			rep, err := Validate([]byte(tc.mutate(t, src)), Options{})
			if err != nil {
				t.Fatal(err)
			}
			var msgs []string
			for _, f := range rep.Schema {
				msgs = append(msgs, f.Message+" at "+f.Path)
			}
			if len(rep.Schema) == 0 {
				t.Fatalf("no schema finding; the defect went unnoticed")
			}
			if !strings.Contains(strings.Join(msgs, "\n"), tc.want) {
				t.Errorf("no finding contains %q; got %v", tc.want, msgs)
			}
			if tc.rulesClean && (rep.Fatal != 0 || rep.Warn != 0) {
				t.Errorf("the rule set was expected to stay silent so the schema check is the only"+
					" evidence, but it reported %d fatal, %d warning: %v", rep.Fatal, rep.Warn, rep.Findings)
			}
		})
	}
}

// TestSchemaCardinalityFollowsTheSchema records a deliberate limit of the
// checker. The D16B schema makes the header monetary summation amounts
// optional and repeatable - minOccurs="0" maxOccurs="unbounded" - so dropping
// or duplicating one of them is valid CII XML, and our model keeps that
// looseness instead of quietly inventing a stricter schema. Those defects
// belong to the business rules, which do object, and the two sets of findings
// are reported apart for exactly that reason.
func TestSchemaCardinalityFollowsTheSchema(t *testing.T) {
	src := string(read(t, filepath.Join(refDir(t), "cii/examples/CII_example1.xml")))
	lt := "<ram:LineTotalAmount>229.6</ram:LineTotalAmount>"
	mustOnce(t, src, lt)

	dropped := strings.Replace(src, lt, "", 1)
	rep, err := Validate([]byte(dropped), Options{})
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Schema) != 0 {
		t.Errorf("the schema allows a missing LineTotalAmount, but we reported %v", rep.Schema)
	}
	if rep.Fatal == 0 {
		t.Error("the business rules were expected to object to a missing LineTotalAmount")
	}

	dup := strings.Replace(src, lt, lt+lt, 1)
	rep, err = Validate([]byte(dup), Options{})
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Schema) != 0 {
		t.Errorf("the schema allows a repeated LineTotalAmount, but we reported %v", rep.Schema)
	}
}

// TestSchemaExtensionContent checks the one place in the UBL schemas where a
// document may carry elements the schema cannot name: cac:ExtensionContent is
// an xs:any wildcard, and most national and community profiles put their own
// business content there. A checker that rejects it rejects valid invoices, so
// this case belongs with the corpora even though no official instance uses it.
func TestSchemaExtensionContent(t *testing.T) {
	const (
		file  = "test/testfiles/Invoice-Max_content.xml"
		mark  = "<cbc:UBLVersionID"
		block = `<ext:UBLExtensions xmlns:ext="urn:oasis:names:specification:ubl:schema:xsd:CommonExtensionComponents-2` +
			`" xmlns:eext="urn:example:ext">` +
			`<ext:UBLExtension><ext:ExtensionContent>` +
			`<eext:Pay><eext:Amount>1.00</eext:Amount><eext:Ref>x</eext:Ref></eext:Pay>` +
			`</ext:ExtensionContent></ext:UBLExtension>` +
			`</ext:UBLExtensions>`
	)
	src := string(read(t, filepath.Join(refDir(t), file)))
	doc := strings.Replace(src, mark, block+mark, 1)
	if doc == src {
		t.Fatalf("%s does not contain %s", file, mark)
	}
	xd, err := xp.ParseXML(doc)
	if err != nil {
		t.Fatal(err)
	}
	fs, err := SchemaFindings(SyntaxUBL, xd)
	if err != nil {
		t.Fatal(err)
	}
	if len(fs) > 0 {
		t.Fatalf("extension content produced %d schema findings: %v", len(fs), fs)
	}
	// An empty extension holds no content element, which the wildcard
	// requires, so it is still a defect worth reporting.
	empty := strings.Replace(src, mark,
		`<ext:UBLExtensions xmlns:ext="urn:oasis:names:specification:ubl:schema:xsd:CommonExtensionComponents-2">`+
			`<ext:UBLExtension><ext:ExtensionContent/></ext:UBLExtension></ext:UBLExtensions>`+mark, 1)
	ed, err := xp.ParseXML(empty)
	if err != nil {
		t.Fatal(err)
	}
	fs, err = SchemaFindings(SyntaxUBL, ed)
	if err != nil {
		t.Fatal(err)
	}
	if len(fs) != 1 || !strings.Contains(fs[0].Message, "missing required content") {
		t.Errorf("empty ExtensionContent: want one finding about missing content, got %v", fs)
	}
}
