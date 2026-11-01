package einvoice_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/dcnws/invoicekit.go"
)

// Speed is a claim a buyer can check, so it is measured in the repository
// rather than asserted in prose. The documents are the official ones, the call
// is the public Validate, and nothing is cached between iterations: parse,
// schema and every business rule run each time. Run it with -cpu 1 so the
// number is one core's worth, which is what a per-document price is priced
// against:
//
//	go test -run '^$' -bench . -benchmem -cpu 1 ./
//
// The UBL corpus is the 29 documents that ship with the official CEF EN 16931
// validation artefacts (22 of them Peppol BIS Billing 3.0); the CII document is
// the XRechnung sample this repository ships. Both are skipped when the corpus
// is not checked out beside it.

// benchCorpus reads every XML file in dir, failing the benchmark if the corpus
// is empty: a benchmark that measured nothing would still report a time.
func benchCorpus(b *testing.B, dir string) [][]byte {
	b.Helper()
	if _, err := os.Stat(dir); err != nil {
		b.Skipf("corpus not available at %s", dir)
	}
	files, err := filepath.Glob(filepath.Join(dir, "*.xml"))
	if err != nil {
		b.Fatalf("glob %s: %v", dir, err)
	}
	if len(files) == 0 {
		b.Fatalf("no documents in %s", dir)
	}
	var docs [][]byte
	var size int
	for _, f := range files {
		data, err := os.ReadFile(f)
		if err != nil {
			b.Fatalf("read %s: %v", f, err)
		}
		docs = append(docs, data)
		size += len(data)
	}
	b.ReportMetric(float64(len(docs)), "docs/op")
	b.SetBytes(int64(size))
	return docs
}

func BenchmarkValidateUBL(b *testing.B) {
	docs := benchCorpus(b, filepath.Join(refDirB(b), "test", "testfiles"))
	b.ReportAllocs()
	for b.Loop() {
		for _, data := range docs {
			if _, err := einvoice.Validate(data, einvoice.Options{}); err != nil {
				b.Fatalf("validate: %v", err)
			}
		}
	}
}

func BenchmarkValidateCII(b *testing.B) {
	data, err := os.ReadFile(filepath.Join("testdata", "samples", "cii-xrechnung.xml"))
	if err != nil {
		b.Fatalf("read the CII sample: %v", err)
	}
	b.ReportMetric(1, "docs/op")
	b.SetBytes(int64(len(data)))
	b.ReportAllocs()
	for b.Loop() {
		if _, err := einvoice.Validate(data, einvoice.Options{}); err != nil {
			b.Fatalf("validate: %v", err)
		}
	}
}

// refDirB is refDir for a benchmark: the corpus checkout, beside this
// repository or named by EINVOICE_REF.
func refDirB(b *testing.B) string {
	b.Helper()
	d := os.Getenv("EINVOICE_REF")
	if d == "" {
		d = "../ref-en16931"
	}
	return d
}
