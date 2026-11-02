package en16931

import "testing"

// TestArtefactInfo checks that the version and update date the European
// Commission stamps into its published Schematrons can be read back out, so a
// verdict can name the artefacts behind it.
func TestArtefactInfo(t *testing.T) {
	for _, sx := range []Syntax{SyntaxCII, SyntaxUBL} {
		version, updated := ArtefactInfo(sx)
		if version == "" {
			t.Fatalf("%s: no artefact version found", sx)
		}
		if !isDotted(version) {
			t.Errorf("%s: artefact version %q does not look like a version", sx, version)
		}
		if updated == "" {
			t.Fatalf("%s: no artefact update date found", sx)
		}
		if len(updated) != 10 || updated[4] != '-' || updated[7] != '-' {
			t.Errorf("%s: update date %q is not YYYY-MM-DD", sx, updated)
		}
	}
	if version, _ := ArtefactInfo(Syntax("nope")); version != "" {
		t.Errorf("unknown syntax returned %q, want empty", version)
	}
}

func isDotted(s string) bool {
	dots, digits := 0, 0
	for _, r := range s {
		switch {
		case r == '.':
			dots++
		case r >= '0' && r <= '9':
			digits++
		default:
			return false
		}
	}
	return dots >= 1 && digits >= 1
}
