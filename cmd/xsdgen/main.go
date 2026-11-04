// Command xsdgen prunes the vendored XSD sets into the compact validity models
// that the library embeds, so a validator build does not carry 3 MB of schema
// documentation.
package main

import (
	"bytes"
	"compress/gzip"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/dcnws/invoicekit.go/xsd"
)

// errUsage marks a command-line mistake. It exits 2 so a script can tell
// "you asked for something impossible" from "the schema went wrong" (exit 1).
var errUsage = errors.New("xsdgen: usage")

type multiFlag []string

func (m *multiFlag) String() string { return strings.Join(*m, ",") }
func (m *multiFlag) Set(v string) error {
	*m = append(*m, v)
	return nil
}

func main() {
	if err := run(os.Args[1:], os.Stdout, os.Stderr); err != nil {
		if errors.Is(err, errUsage) {
			os.Exit(2)
		}
		os.Exit(1)
	}
}

// run is the whole command with its arguments, output and error stream passed
// in, so the generator can be exercised in a test instead of only as a process.
func run(args []string, stdout, stderr io.Writer) error {
	var files, roots multiFlag
	fs := flag.NewFlagSet("xsdgen", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Var(&files, "schema", "schema document to read (repeatable)")
	fs.Var(&roots, "root", "root element in Clark notation, {ns}local (repeatable)")
	out := fs.String("out", "", "output file, gzipped when the name ends in .gz")
	dir := fs.String("dir", ".", "directory to resolve imports against")
	if err := fs.Parse(args); err != nil {
		return fmt.Errorf("%w: %v", errUsage, err)
	}
	if len(files) == 0 || len(roots) == 0 || *out == "" {
		fmt.Fprintln(stderr, "usage: xsdgen -dir DIR -schema FILE -root {ns}Name -out FILE")
		return fmt.Errorf("%w: -schema, -root and -out are all required", errUsage)
	}
	m, err := xsd.LoadMulti(*dir, files)
	if err != nil {
		return fmt.Errorf("load: %w", err)
	}
	pruned, err := m.Prune(roots)
	if err != nil {
		return fmt.Errorf("prune: %w", err)
	}
	var buf bytes.Buffer
	if err := pruned.Write(&buf); err != nil {
		return fmt.Errorf("write: %w", err)
	}
	data := buf.Bytes()
	if strings.HasSuffix(*out, ".gz") {
		var z bytes.Buffer
		zw := gzip.NewWriter(&z)
		if _, err := zw.Write(data); err != nil {
			return fmt.Errorf("gzip: %w", err)
		}
		if err := zw.Close(); err != nil {
			return fmt.Errorf("gzip: %w", err)
		}
		data = z.Bytes()
	}
	if err := os.WriteFile(*out, data, 0o644); err != nil {
		return fmt.Errorf("write: %w", err)
	}
	fmt.Fprintf(stdout, "%s: %d decls, %d types, %d bytes raw, %d bytes written\n",
		*out, len(pruned.Decls), len(pruned.Types), buf.Len(), len(data))
	return nil
}
