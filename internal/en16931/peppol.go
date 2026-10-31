package en16931

import (
	"bytes"
	"compress/gzip"
	"embed"
	"fmt"
	"io"
	"sort"

	"github.com/dcnws/invoicekit.go/internal/sch"
)

//go:embed schemas/peppol
var profileFS embed.FS

// Profile names a published rule-set bundle: a set of artefacts that a network
// or community validates against as a whole, rather than one add-on rule set.
// It differs from a CIUS in that it ships its own copy of the EN 16931
// assertions, so validating against a profile replaces the base rule sets
// instead of adding to them. That keeps the answer a caller gets in step with
// what the network itself would say, artefact versions included.
type Profile string

const (
	// ProfilePeppol is Peppol BIS Billing 3.0, the invoice profile the Peppol
	// network carries. Its artefacts are the CEN EN 16931 Schematrons plus the
	// rules OpenPEPPOL adds (PEPPOL-EN16931-*, PEPPOL-COMMON-*) and the country
	// rules shipped alongside them (DE-, DK-, GR-, IT-, NL-, NO-, SE-).
	ProfilePeppol Profile = "peppol"
)

// peppolRoot is the embed root inside this module.
const peppolRoot = "schemas/peppol"

// artefact is one Schematron file of a profile: where it is embedded, and the
// name every finding it produces carries, so a caller can tell the norm's own
// assertions from the network's.
type artefact struct {
	file string
	name string
}

// profileArtefacts lists the artefacts of a profile per syntax, in the order
// they run. The names mirror the upstream file names closely enough to be
// recognisable in a report while staying short.
var profileArtefacts = map[Profile]map[Syntax][]artefact{
	ProfilePeppol: {
		SyntaxCII: {
			{file: "CEN-EN16931-CII.sch.gz", name: "peppol-cen"},
			{file: "PEPPOL-EN16931-CII.sch.gz", name: "peppol"},
		},
		SyntaxUBL: {
			{file: "CEN-EN16931-UBL.sch.gz", name: "peppol-cen"},
			{file: "PEPPOL-EN16931-UBL.sch.gz", name: "peppol"},
		},
	},
}

// profileVersion records which upstream release each embedded bundle came from.
// Keep it in step with NOTICE.md: the artefacts are redistributed unmodified.
var profileVersion = map[Profile]string{
	ProfilePeppol: "Peppol BIS Billing 3.0 release 3.0.21 (May 2026), " +
		"with EN 16931 Schematron 1.3.16",
}

// ProfileNames lists every rule-set bundle the library carries, sorted.
func ProfileNames() []string {
	var out []string
	for p := range profileArtefacts {
		out = append(out, string(p))
	}
	sort.Strings(out)
	return out
}

// ProfileVersion reports the upstream release a profile bundle came from, or ""
// when the name is not one this library carries.
func ProfileVersion(p Profile) string { return profileVersion[p] }

// ProfilePhaseIDs lists the phase ids of the EN 16931 artefact of a profile.
// The network artefacts declare no phases of their own: they run in full every
// time.
func ProfilePhaseIDs(p Profile, sx Syntax) ([]string, error) {
	sets, err := ProfileRuleSets(p, sx)
	if err != nil {
		return nil, err
	}
	return sets[0].PhaseIDs(), nil
}

// ProfileRuleSets returns the parsed and compiled artefacts of a profile for a
// syntax, in the order they should run. Like every other rule set here, the
// result is cached and the artefacts are embedded, so there is nothing to
// download and nothing to install.
func ProfileRuleSets(p Profile, sx Syntax) ([]*sch.Schema, error) {
	arts, ok := profileArtefacts[p]
	if !ok {
		return nil, fmt.Errorf("en16931: unknown profile %q (available: %v)", p, ProfileNames())
	}
	list, ok := arts[sx]
	if !ok {
		return nil, fmt.Errorf("en16931: profile %s has no rule set for syntax %q (want CII or UBL)", p, sx)
	}
	out := make([]*sch.Schema, 0, len(list))
	for _, a := range list {
		s, err := cached("profile|"+string(p)+"|"+string(sx)+"|"+a.file, func() (*sch.Schema, error) {
			raw, err := profileFS.ReadFile(peppolRoot + "/" + a.file)
			if err != nil {
				return nil, err
			}
			data, err := gunzip(a.file, raw)
			if err != nil {
				return nil, err
			}
			s, err := sch.Parse(data)
			if err != nil {
				return nil, fmt.Errorf("en16931: profile %s %s: %w", p, a.name, err)
			}
			s.Name = a.name
			if err := s.Compile(); err != nil {
				return nil, fmt.Errorf("en16931: profile %s %s: %w", p, a.name, err)
			}
			if bad := s.CompileErrors(); len(bad) > 0 {
				return nil, fmt.Errorf("en16931: profile %s %s: %d rule(s) could not be compiled, first: %s",
					p, a.name, len(bad), bad[0])
			}
			return s, nil
		})
		if err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, nil
}

// gunzip inflates one embedded artefact. The profile Schematrons are half a
// megabyte each in the CEN case, which is worth the few lines of decompression.
func gunzip(name string, raw []byte) ([]byte, error) {
	zr, err := gzip.NewReader(bytes.NewReader(raw))
	if err != nil {
		return nil, fmt.Errorf("en16931: %s: %w", name, err)
	}
	src, err := io.ReadAll(io.LimitReader(zr, 1<<26))
	if err != nil {
		return nil, fmt.Errorf("en16931: %s: %w", name, err)
	}
	if err := zr.Close(); err != nil {
		return nil, fmt.Errorf("en16931: %s: %w", name, err)
	}
	return src, nil
}
