// Command xsdgen prunes the vendored XSD sets into the compact validity models
// that the library embeds, so a validator build does not carry 3 MB of schema
// documentation.
package main

import (
	"bytes"
	"compress/gzip"
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/dcnws/invoicekit.go/xsd"
)

type multiFlag []string

func (m *multiFlag) String() string { return strings.Join(*m, ",") }
func (m *multiFlag) Set(v string) error {
	*m = append(*m, v)
	return nil
}

func main() {
	var files, roots multiFlag
	flag.Var(&files, "schema", "schema document to read (repeatable)")
	flag.Var(&roots, "root", "root element in Clark notation, {ns}local (repeatable)")
	out := flag.String("out", "", "output file, gzipped when the name ends in .gz")
	dir := flag.String("dir", ".", "directory to resolve imports against")
	flag.Parse()
	if len(files) == 0 || len(roots) == 0 || *out == "" {
		fmt.Fprintln(os.Stderr, "usage: xsdgen -dir DIR -schema FILE -root {ns}Name -out FILE")
		os.Exit(2)
	}
	m, err := xsd.LoadMulti(*dir, files)
	if err != nil {
		fmt.Fprintln(os.Stderr, "load:", err)
		os.Exit(1)
	}
	pruned, err := m.Prune(roots)
	if err != nil {
		fmt.Fprintln(os.Stderr, "prune:", err)
		os.Exit(1)
	}
	var buf bytes.Buffer
	if err := pruned.Write(&buf); err != nil {
		fmt.Fprintln(os.Stderr, "write:", err)
		os.Exit(1)
	}
	data := buf.Bytes()
	if strings.HasSuffix(*out, ".gz") {
		var z bytes.Buffer
		gz := gzip.NewWriter(&z)
		if _, err := gz.Write(data); err != nil {
			fmt.Fprintln(os.Stderr, "gzip:", err)
			os.Exit(1)
		}
		if err := gz.Close(); err != nil {
			fmt.Fprintln(os.Stderr, "gzip:", err)
			os.Exit(1)
		}
		data = z.Bytes()
	}
	if err := os.WriteFile(*out, data, 0o644); err != nil {
		fmt.Fprintln(os.Stderr, "write:", err)
		os.Exit(1)
	}
	fmt.Printf("%s: %d decls, %d types, %d bytes raw, %d bytes written\n",
		*out, len(pruned.Decls), len(pruned.Types), buf.Len(), len(data))
}
