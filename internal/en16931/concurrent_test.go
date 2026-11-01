package en16931

import (
	"testing"

	"github.com/dcnws/invoicekit.go/internal/xp"
	"github.com/dcnws/invoicekit.go/xsd"
)

// TestSchemaModelConcurrency validates one document against a fresh schema
// model from several goroutines at once. The hosted API does exactly that: the
// model is loaded once and shared, so the validation path must be read-only.
// Anything the matcher fills in while it runs is a data race between requests.
func TestSchemaModelConcurrency(t *testing.T) {
	name := xsdFile[SyntaxUBL]
	raw, err := xsdFS.ReadFile(name)
	if err != nil {
		t.Fatalf("read %s: %v", name, err)
	}
	src, err := gunzip(name, raw)
	if err != nil {
		t.Fatalf("gunzip %s: %v", name, err)
	}
	// A cold model: the first validation must not be the one that compiles the
	// content models while its neighbours are reading them.
	m, err := xsd.Load(name, src, nil)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	doc, err := xp.ParseXML(string(read(t, "../../testdata/samples/ubl-peppol.xml")))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	const runs = 10
	got := make([][]xsd.Finding, 8)
	done := make(chan int, 8)
	for i := range got {
		go func(i int) {
			for j := 0; j < runs; j++ {
				got[i] = m.Validate(doc)
			}
			done <- i
		}(i)
	}
	for range got {
		<-done
	}
	for i, fs := range got {
		if len(fs) != 0 {
			t.Fatalf("run %d reports %d schema findings: %v", i, len(fs), fs[0])
		}
	}
}
