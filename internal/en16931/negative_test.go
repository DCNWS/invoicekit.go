package en16931

import (
	"regexp"
	"strings"
	"testing"
)

// ublBase and ciiBase are the two instances the negative tests mutate. Both are
// official reference documents that validate cleanly.
const (
	ublBase = "/test/testfiles/Invoice-Min_content_with_VAT.xml"
	ciiBase = "/cii/examples/CII_example1.xml"
)

// ciiCorpus are the CI/UN-CEFACT examples shipped with the official rule sets.
// They are illustrative rather than certified, but every one of them passes the
// official Schematrons, so they double as a positive corpus for the CII side.
func TestCIIExamples(t *testing.T) {
	dir := refDir(t) + "/cii/examples"
	files := glob(t, dir, "*.xml")
	if len(files) == 0 {
		t.Skipf("no CII examples in %s", dir)
	}
	for _, f := range files {
		rep, err := Validate(read(t, f), Options{})
		if err != nil {
			t.Errorf("%s: %v", f, err)
			continue
		}
		if len(rep.Findings) > 0 {
			t.Errorf("%s: unexpected findings %v", f, rep.IDs())
		}
	}
	t.Logf("%d CII documents validated", len(files))
}

// A mutation is a single targeted edit to a valid instance that should make the
// listed business rules fire. Without tests like these a validator that simply
// reported nothing at all would pass the positive corpus.
type mutation struct {
	name string
	base string // reference file to mutate, relative to the reference root
	from string
	to   string
	re   bool // from is a regular expression rather than a literal anchor
	want []string
}

var mutations = []mutation{
	{
		name: "missing invoice number",
		base: ublBase,
		from: "\t<cbc:ID>2018-112</cbc:ID>\n",
		want: []string{"BR-02"},
	},
	{
		name: "missing buyer party",
		base: ublBase,
		from: `\t<cac:AccountingCustomerParty>[\s\S]*?</cac:AccountingCustomerParty>\n`,
		re:   true,
		want: []string{"BR-07", "BR-10"},
	},
	{
		name: "missing seller party",
		base: ublBase,
		from: `\t<cac:AccountingSupplierParty>[\s\S]*?</cac:AccountingSupplierParty>\n`,
		re:   true,
		want: []string{"BR-06", "BR-08", "BR-S-02"},
	},
	{
		name: "no invoice line",
		base: ublBase,
		from: `\t<cac:InvoiceLine>[\s\S]*?</cac:InvoiceLine>\n`,
		re:   true,
		want: []string{"BR-16", "BR-S-01"},
	},
	{
		name: "gross total inconsistent with net plus VAT",
		base: ublBase,
		from: `<cbc:TaxInclusiveAmount currencyID="SEK">500</cbc:TaxInclusiveAmount>`,
		to:   `<cbc:TaxInclusiveAmount currencyID="SEK">450</cbc:TaxInclusiveAmount>`,
		want: []string{"BR-CO-15"},
	},
	{
		name: "VAT total inconsistent with the category breakdown",
		base: ublBase,
		from: `<cbc:TaxAmount currencyID="SEK">100</cbc:TaxAmount>`,
		to:   `<cbc:TaxAmount currencyID="SEK">90</cbc:TaxAmount>`,
		want: []string{"BR-CO-14", "BR-CO-15"},
	},
	{
		name: "VAT category amount inconsistent with its rate",
		base: ublBase,
		from: `<cbc:Percent>25</cbc:Percent>`,
		to:   `<cbc:Percent>20</cbc:Percent>`,
		want: []string{"BR-S-09"},
	},
	{
		name: "header net total inconsistent with the line sums",
		base: ublBase,
		from: `<cbc:LineExtensionAmount currencyID="SEK">400</cbc:LineExtensionAmount>`,
		to:   `<cbc:LineExtensionAmount currencyID="SEK">350</cbc:LineExtensionAmount>`,
		want: []string{"BR-CO-10", "BR-CO-13"},
	},
	{
		name: "VAT category taxable amount inconsistent with the line sums",
		base: ublBase,
		from: `<cbc:TaxableAmount currencyID="SEK">400</cbc:TaxableAmount>`,
		to:   `<cbc:TaxableAmount currencyID="SEK">380</cbc:TaxableAmount>`,
		want: []string{"BR-S-08", "BR-S-09"},
	},
	{
		name: "CII line totals inconsistent with the header",
		base: ciiBase,
		from: `<ram:LineTotalAmount>229.6</ram:LineTotalAmount>`,
		to:   `<ram:LineTotalAmount>200</ram:LineTotalAmount>`,
		want: []string{"BR-CO-10", "BR-CO-13"},
	},
}

// TestMutations checks that defects are reported, not just that valid documents
// are accepted.
func TestMutations(t *testing.T) {
	cases := 0
	for _, m := range mutations {
		doc := string(read(t, refDir(t)+m.base))
		if m.re {
			re, err := regexp.Compile(m.from)
			if err != nil {
				t.Fatalf("%s: %v", m.name, err)
			}
			idx := re.FindStringIndex(doc)
			if idx == nil {
				t.Fatalf("%s: pattern not found in base document", m.name)
			}
			doc = doc[:idx[0]] + m.to + doc[idx[1]:]
		} else {
			if !strings.Contains(doc, m.from) {
				t.Fatalf("%s: anchor not found in base document", m.name)
			}
			doc = strings.Replace(doc, m.from, m.to, 1)
		}

		rep, err := Validate([]byte(doc), Options{})
		if err != nil {
			t.Errorf("%s: %v", m.name, err)
			continue
		}
		for _, fi := range rep.Findings {
			if fi.Err != "" {
				t.Errorf("%s: rule %s could not be evaluated: %s", m.name, fi.ID, fi.Err)
			}
		}
		fired := map[string]bool{}
		for _, fi := range rep.Findings {
			fired[fi.ID] = true
		}
		for _, id := range m.want {
			if !fired[id] {
				t.Errorf("%s: expected %s to fire, got %v", m.name, id, rep.IDs())
			}
		}
		if rep.Valid() {
			t.Errorf("%s: document was reported valid", m.name)
		}
		cases++
	}
	t.Logf("%d mutation cases", cases)
}
