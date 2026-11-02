package en16931

import (
	"regexp"
)

// artefactHeaderLimit bounds how far into a Schematron the Commission's version
// comment is searched for. It sits within the first few hundred bytes of both
// published files, so reading the whole rule set to find it would be waste.
const artefactHeaderLimit = 4096

// The Commission stamps the release of its Schematrons into a comment at the
// top of each published file, in the form:
//
//	<!--Schematron version 1.3.17 - Last update: 2026-10-09-->
var (
	artefactVersionRE = regexp.MustCompile(`Schematron version ([0-9]+(?:\.[0-9]+)*)`)
	artefactUpdateRE  = regexp.MustCompile(`Last update: ([0-9]{4}-[0-9]{2}-[0-9]{2})`)
)

// ArtefactInfo reports the release version and the last-update date the
// European Commission stamped into the published Schematron of a syntax, so a
// verdict can name the artefacts behind it. Both are the empty string for an
// unknown syntax, and the date is empty if the file carries no stamp.
func ArtefactInfo(sx Syntax) (version, updated string) {
	name, ok := schemaFile[sx]
	if !ok {
		return "", ""
	}
	data, err := schemaFS.ReadFile(name)
	if err != nil {
		return "", ""
	}
	head := string(data)
	if len(head) > artefactHeaderLimit {
		head = head[:artefactHeaderLimit]
	}
	if m := artefactVersionRE.FindStringSubmatch(head); len(m) == 2 {
		version = m[1]
	}
	if m := artefactUpdateRE.FindStringSubmatch(head); len(m) == 2 {
		updated = m[1]
	}
	return version, updated
}
