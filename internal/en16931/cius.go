package en16931

import (
	"embed"
	"fmt"
	"path"
	"sort"

	"github.com/dcnws/invoicekit.go/internal/sch"
)

//go:embed schemas/cius
var ciusFS embed.FS

// CIUS names a rule set that customises EN 16931 for one country or network.
// EN 16931 calls this a CIUS (Customisation Information User Segment): it may
// restrict the norm and add rules, but it may never relax it, so validating
// against a CIUS means running the EN 16931 rules and that rule set together.
// The declaration travels in the document itself, in BT-24.
type CIUS string

const (
	// CIUSXRechnung is the German XRechnung CIUS used by public administration
	// invoicing (see xeinkauf.de). Its rules are the BR-DE, BR-DE-CVD,
	// BR-DEX and BR-TMP assertions.
	CIUSXRechnung CIUS = "xrechnung"
)

// ciusSource maps a rule set to its embedded entry point per syntax, relative
// to the schemas/cius directory. The layout mirrors the upstream release, so
// the <include href="../common.sch"> of the shipped files resolves unmodified.
var ciusSource = map[CIUS]map[Syntax]string{
	CIUSXRechnung: {
		SyntaxCII: "xrechnung/cii/XRechnung-CII-validation.sch",
		SyntaxUBL: "xrechnung/ubl/XRechnung-UBL-validation.sch",
	},
}

// ciusRoot is the embed root inside this module.
const ciusRoot = "schemas/cius"

// ciusVersion records which upstream release each embedded rule set came from.
// Keep it in step with NOTICE.md: rule sets are redistributed unmodified.
var ciusVersion = map[CIUS]string{
	CIUSXRechnung: "2.6.0, for XRechnung 3.0.2",
}

// CiusNames lists every CIUS rule set the library carries, sorted.
func CiusNames() []string {
	var out []string
	for c := range ciusSource {
		out = append(out, string(c))
	}
	sort.Strings(out)
	return out
}

// CiusVersion reports the upstream version of a CIUS rule set, or "" when the
// name is not one this library carries.
func CiusVersion(c CIUS) string { return ciusVersion[c] }

// CiusPhaseIDs lists the phase ids of a CIUS rule set for a syntax.
func CiusPhaseIDs(c CIUS, sx Syntax) ([]string, error) {
	s, err := CiusSchematron(c, sx)
	if err != nil {
		return nil, err
	}
	return s.PhaseIDs(), nil
}

// CiusSchematron returns the parsed and compiled CIUS rule set. Like the
// EN 16931 one, the result is cached and the whole rule set is embedded, so
// there is nothing to download and nothing to install.
func CiusSchematron(c CIUS, sx Syntax) (*sch.Schema, error) {
	files, ok := ciusSource[c]
	if !ok {
		return nil, fmt.Errorf("en16931: unknown CIUS %q (available: %v)", c, CiusNames())
	}
	name, ok := files[sx]
	if !ok {
		return nil, fmt.Errorf("en16931: CIUS %s has no rule set for syntax %q (want CII or UBL)", c, sx)
	}
	return cached(string(c)+"|"+string(sx), func() (*sch.Schema, error) {
		data, err := ciusFS.ReadFile(ciusRoot + "/" + name)
		if err != nil {
			return nil, err
		}
		dir := path.Dir(name)
		s, err := sch.ParseWith(data, func(href string) ([]byte, error) {
			return ciusFS.ReadFile(path.Join(ciusRoot, dir, href))
		})
		if err != nil {
			return nil, fmt.Errorf("en16931: %s %s rule set: %w", c, sx, err)
		}
		s.Name = string(c)
		if err := s.Compile(); err != nil {
			return nil, fmt.Errorf("en16931: %s %s rule set: %w", c, sx, err)
		}
		if bad := s.CompileErrors(); len(bad) > 0 {
			return nil, fmt.Errorf("en16931: %s %s rule set: %d rule(s) could not be compiled, first: %s",
				c, sx, len(bad), bad[0])
		}
		return s, nil
	})
}
