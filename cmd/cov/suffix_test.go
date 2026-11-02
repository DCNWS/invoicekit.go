package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dcnws/invoicekit.go/internal/en16931"
)

// TestVariantsUnderExistingPrefix: an assert that forbids a path whose first
// steps the document already contains must grow the element that is there, not
// add a second copy of it. UBL forbids Delivery/DeliveryParty/PartyIdentification
// and the sample has a Delivery, so the variants must place DeliveryParty inside
// that Delivery: a second Delivery would break the schema, which allows one.
func TestVariantsUnderExistingPrefix(t *testing.T) {
	doc, err := os.ReadFile(filepath.Join("..", "..", "testdata", "samples", "ubl-peppol.xml"))
	if err != nil {
		t.Fatal(err)
	}
	s, err := en16931.Schematron(en16931.SyntaxUBL)
	if err != nil {
		t.Fatal(err)
	}
	smap, dmap := s.NS, docPrefixes(doc)
	m, err := en16931.SchemaModel(en16931.SyntaxUBL)
	if err != nil {
		t.Fatal(err)
	}
	ctx := "/ubl:Invoice | /cn:CreditNote"
	chain := "cac:Delivery/cac:DeliveryParty/cac:PartyIdentification"
	before := bytes.Count(doc, []byte("<cac:Delivery>"))
	vs := variantsAt(m, doc, ctx, chain, smap, dmap)
	if len(vs) == 0 {
		t.Fatal("no variants")
	}
	for i, v := range vs {
		if got := bytes.Count(v, []byte("<cac:Delivery>")); got != before {
			t.Fatalf("variant %d adds a Delivery: %d copies, want %d", i, got, before)
		}
		if !bytes.Contains(v, []byte("<cac:DeliveryParty>")) {
			t.Fatalf("variant %d has no DeliveryParty: %s", i, firstLines(v))
		}
		dl := bytes.Index(v, []byte("<cac:Delivery>"))
		de := bytes.Index(v, []byte("</cac:Delivery>"))
		dp := bytes.Index(v, []byte("<cac:DeliveryParty>"))
		if dp < dl || dp > de {
			t.Fatalf("variant %d: DeliveryParty outside the existing Delivery", i)
		}
	}
	// And one of them must survive the schema and make UBL-CR-392 speak.
	keep, ids := firstFiring(en16931.SyntaxUBL, "UBL-CR-392", vs, 8)
	if keep == nil {
		t.Fatalf("no variant fires UBL-CR-392 (%d variants)", len(vs))
	}
	if !strings.Contains(string(keep), "PartyIdentification") {
		t.Errorf("kept mutant lost the leaf: %s", firstLines(keep))
	}
	t.Logf("ids %v", ids)
}

// TestFragsOf: without a schema model a chain is rendered twice, with a
// placeholder value at the leaf and with the leaf empty, because the schematron
// paths do not say which shape the schema expects and only one of the two can
// be valid.
func TestFragsOf(t *testing.T) {
	_, steps, ok := parsePath("ram:A/ram:B")
	if !ok {
		t.Fatal("chain did not parse")
	}
	got := fragsOf(nil, steps, nil, nil)
	want := []string{"<ram:A><ram:B>x</ram:B></ram:A>", "<ram:A><ram:B/></ram:A>"}
	if len(got) != len(want) {
		t.Fatalf("frags: %q", got)
	}
	for i := range want {
		if string(got[i]) != want[i] {
			t.Errorf("frag %d = %q, want %q", i, got[i], want[i])
		}
	}
}

// TestSchemaFrag: the schema model knows what an element must contain, so a
// fragment built from it carries those children instead of guessing. UBL's
// PartyIdentification is a sequence whose ID is required, which is what made
// every hand written fragment for UBL-CR-392 invalid. A chain the model does
// not describe, or a step carrying a predicate, is refused.
func TestSchemaFrag(t *testing.T) {
	doc, err := os.ReadFile(filepath.Join("..", "..", "testdata", "samples", "ubl-peppol.xml"))
	if err != nil {
		t.Fatal(err)
	}
	s, err := en16931.Schematron(en16931.SyntaxUBL)
	if err != nil {
		t.Fatal(err)
	}
	m, err := en16931.SchemaModel(en16931.SyntaxUBL)
	if err != nil {
		t.Fatal(err)
	}
	_, steps, ok := parsePath("cac:DeliveryParty/cac:PartyIdentification")
	if !ok {
		t.Fatal("chain did not parse")
	}
	f := schemaFrag(m, s.NS, docPrefixes(doc), steps)
	if f == nil {
		t.Fatal("the model refused a chain it describes")
	}
	if !bytes.Contains(f, []byte("<cbc:ID>")) {
		t.Errorf("fragment has no required ID: %s", f)
	}
	if !bytes.HasPrefix(f, []byte("<cac:DeliveryParty>")) || !bytes.HasSuffix(f, []byte("</cac:DeliveryParty>")) {
		t.Errorf("fragment is not wrapped in DeliveryParty: %s", f)
	}
	if !bytes.HasSuffix(f, []byte("</cac:PartyIdentification></cac:DeliveryParty>")) {
		t.Errorf("fragment trails past the chain: %s", f)
	}
	// A predicate cannot be expressed by a content model.
	_, pred, ok := parsePath("cac:DeliveryParty[cac:PartyIdentification]/cac:PartyName")
	if !ok {
		t.Fatal("predicated chain did not parse")
	}
	if f := schemaFrag(m, s.NS, docPrefixes(doc), pred); f != nil {
		t.Errorf("a predicated chain was rendered: %s", f)
	}
	// Neither can an element the model has never heard of.
	_, ghost, ok := parsePath("cac:DeliveryParty/cac:ImaginaryThing")
	if !ok {
		t.Fatal("invented chain did not parse")
	}
	if f := schemaFrag(m, s.NS, docPrefixes(doc), ghost); f != nil {
		t.Errorf("an element outside the model was rendered: %s", f)
	}
}

// TestVariantsSuffixDepth: the suffix is what the pass has to invent, so a
// chain that runs out more than four elements deep is refused rather than
// guessed at.
func TestVariantsSuffixDepth(t *testing.T) {
	b := []byte(tdoc)
	if vs := variantsAt(nil, b, "//ram:Period", "ram:A/ram:B/ram:C/ram:D/ram:E", nil, nil); len(vs) == 0 {
		t.Fatal("a four element suffix produced nothing")
	}
	if vs := variantsAt(nil, b, "//ram:Period", "ram:A/ram:B/ram:C/ram:D/ram:E/ram:F", nil, nil); len(vs) != 0 {
		t.Fatalf("a five element suffix produced %d variants", len(vs))
	}
	// A chain the document already contains has nothing left to grow.
	if vs := variantsAt(nil, b, "//ram:Period", "ram:B/ram:C/ram:D", nil, nil); len(vs) != 0 {
		t.Fatalf("an existing chain produced %d variants", len(vs))
	}
}
