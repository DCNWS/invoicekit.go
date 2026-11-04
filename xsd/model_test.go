package xsd

import (
	"strings"
	"testing"

	"github.com/dcnws/invoicekit.go/internal/xp"
)

// TestFindingString covers the one-line form every caller prints.
func TestFindingString(t *testing.T) {
	for _, tc := range []struct {
		f    Finding
		want string
	}{
		{Finding{Path: "/root/a", Message: "takes a text value"}, "/root/a: takes a text value"},
		{Finding{Message: "document has no root element"}, "document has no root element"},
	} {
		if got := tc.f.String(); got != tc.want {
			t.Errorf("Finding.String() = %q, want %q", got, tc.want)
		}
	}
}

// TestKindNames pins the labels that appear in reader and resolver messages.
func TestKindNames(t *testing.T) {
	for _, tc := range []struct {
		k    Kind
		want string
	}{
		{Elem, "element"},
		{Seq, "sequence"},
		{Choice, "choice"},
		{Ref, "ref"},
		{Any, "any"},
		{Kind(200), "?"},
	} {
		if got := tc.k.String(); got != tc.want {
			t.Errorf("Kind(%d).String() = %q, want %q", uint8(tc.k), got, tc.want)
		}
	}
}

// TestTermContent checks the accessor the matcher uses to decide whether an
// accepted element may hold child elements.
func TestTermContent(t *testing.T) {
	inner := &Term{Kind: Seq}
	cases := []struct {
		name string
		term *Term
		want *Term
	}{
		{"typed element", &Term{Kind: Elem, Type: &Type{ID: "{urn:t}X", Term: inner}}, inner},
		{"leaf element", &Term{Kind: Elem}, nil},
		{"empty complex type", &Term{Kind: Elem, Type: &Type{ID: "{urn:t}Y"}}, nil},
		{"resolved ref", &Term{Kind: Ref, Type: &Type{Term: inner}}, inner},
		{"unresolved ref", &Term{Kind: Ref}, nil},
	}
	for _, tc := range cases {
		if got := tc.term.Content(); got != tc.want {
			t.Errorf("%s: Content() = %v, want %v", tc.name, got, tc.want)
		}
	}
	var none *Term
	if none.Content() != nil {
		t.Error("a nil term reports content")
	}
}

// TestClarkNames covers the name form every map in the package is keyed by.
func TestClarkNames(t *testing.T) {
	for _, tc := range []struct{ ns, local, want string }{
		{"urn:t", "x", "{urn:t}x"},
		{"", "x", "x"},
	} {
		if got := clark(tc.ns, tc.local); got != tc.want {
			t.Errorf("clark(%q, %q) = %q, want %q", tc.ns, tc.local, got, tc.want)
		}
		ns, local := splitClark(tc.want)
		if ns != tc.ns || local != tc.local {
			t.Errorf("splitClark(%q) = %q, %q, want %q, %q", tc.want, ns, local, tc.ns, tc.local)
		}
	}
	for _, id := range []string{"", "x", "{broken", "{urn:t}"} {
		if _, l := splitClark(id); l != strings.TrimSuffix(strings.TrimPrefix(id, "{urn:t}"), "}") && id != "{broken" && id != "{urn:t}" {
			t.Errorf("splitClark(%q) mis-split", id)
		}
	}
	if got := local("{urn:t}x"); got != "x" {
		t.Errorf("local = %q, want x", got)
	}
	if got := show("{urn:t}Root"); got != "Root" {
		t.Errorf("show = %q, want Root", got)
	}
	if got := show("no-clark"); got != "no-clark" {
		t.Errorf("show of a bare name = %q, want it unchanged", got)
	}
}

// TestPruneKeepsReachable prunes a loaded model: the unreachable type must go,
// the components a root needs must stay, and they must be the same objects.
func TestPruneKeepsReachable(t *testing.T) {
	m := model(t, seqSrc)
	p, err := m.Prune([]string{"{urn:t}root"})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := p.Types["{urn:t}Pair"]; ok {
		t.Error("Pair survived pruning although nothing refers to it")
	}
	if len(p.Decls) != 1 {
		t.Errorf("pruned model has %d declarations, want 1", len(p.Decls))
	}
	if p.Types["{urn:t}Root"] != m.Types["{urn:t}Root"] {
		t.Error("pruning copied the type instead of sharing it")
	}
	if got := strings.Join(p.Roots, ","); got != "{urn:t}root" {
		t.Errorf("pruned roots = %q, want {urn:t}root", got)
	}
	if p.Source != m.Source {
		t.Errorf("pruned source = %q, want %q", p.Source, m.Source)
	}
	wantClean(t, check(t, p, `<root xmlns="urn:t"><a/><c/></root>`))
}

// TestPruneFollowsRefs checks that a content model referring to a global
// element by ref pulls that declaration in: without it the pruned model could
// not validate the same documents.
func TestPruneFollowsRefs(t *testing.T) {
	m := model(t, refSrc)
	p, err := m.Prune([]string{"{urn:t}root"})
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"{urn:t}root", "{urn:t}d1", "{urn:t}d2"} {
		if _, ok := p.Decls[id]; !ok {
			t.Errorf("%s was pruned away", show(id))
		}
	}
}

// TestPruneUnknownRoot is a caller error, not a schema error.
func TestPruneUnknownRoot(t *testing.T) {
	m := model(t, seqSrc)
	if _, err := m.Prune([]string{"{urn:t}nope"}); err == nil || !strings.Contains(err.Error(), "root element nope is not declared") {
		t.Fatalf("want an unknown-root error, got %v", err)
	}
}

// TestPruneHole checks that a ref whose declaration fell outside the pruned set
// is reported instead of left dangling.
func TestPruneHole(t *testing.T) {
	m := NewModel()
	leaf := &Type{ID: "{urn:t}Leaf"}
	m.Types[leaf.ID] = leaf
	gone := &Decl{ID: "{urn:t}gone", NS: "urn:t", Type: leaf}
	m.Decls[gone.ID] = gone
	holder := &Term{Kind: Elem, Name: gone.ID, DeclRef: gone.ID, Min: 1, Max: 1}
	root := &Decl{ID: "{urn:t}root", NS: "urn:t", Type: &Type{
		ID:   "{urn:t}R",
		Term: &Term{Kind: Seq, Min: 1, Max: 1, Parts: []*Term{holder}},
	}}
	m.Decls[root.ID] = root
	if _, err := m.Prune([]string{root.ID}); err == nil || !strings.Contains(err.Error(), "pruned model refers to undeclared element gone") {
		t.Fatalf("want a hole error, got %v", err)
	}
}

// TestPruneHandlesRecursion covers a type that contains itself: the walk must
// terminate instead of exhausting the stack.
func TestPruneHandlesRecursion(t *testing.T) {
	m := NewModel()
	self := &Type{ID: "{urn:t}Self"}
	m.Types[self.ID] = self
	child := &Term{Kind: Elem, Name: self.ID, Min: 0, Max: -1, Type: self}
	self.Term = &Term{Kind: Seq, Min: 1, Max: 1, Parts: []*Term{child}}
	root := &Decl{ID: "{urn:t}root", NS: "urn:t", Type: self}
	m.Decls[root.ID] = root
	p, err := m.Prune([]string{root.ID})
	if err != nil {
		t.Fatal(err)
	}
	if p.Types[self.ID] != self {
		t.Error("the recursive type is missing from the pruned model")
	}
}

// edgeSrc exercises the shapes the writer has to render that the other fixtures
// miss: a wildcard, an inline anonymous type, occurrence counts, a ref, and a
// child that does not sit in the target namespace.
const edgeSrc = `<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema"
    targetNamespace="urn:t" xmlns:t="urn:t" elementFormDefault="qualified">
  <xs:element name="d1" type="xs:string"/>
  <xs:element name="root" type="t:R"/>
  <xs:complexType name="R">
    <xs:sequence minOccurs="0" maxOccurs="unbounded">
      <xs:element ref="t:d1"/>
      <xs:element name="plain" form="unqualified" type="xs:string"/>
      <xs:element name="inline">
        <xs:complexType><xs:sequence><xs:element name="deep" type="xs:string"/></xs:sequence></xs:complexType>
      </xs:element>
      <xs:any namespace="##other" minOccurs="0"/>
    </xs:sequence>
  </xs:complexType>
</xs:schema>`

// TestWriteEmitsEveryShape checks the generated model carries every decision
// the reader made, and that loading it back gives the same verdicts.
func TestWriteEmitsEveryShape(t *testing.T) {
	m := model(t, edgeSrc)
	var sb strings.Builder
	if err := m.Write(&sb); err != nil {
		t.Fatal(err)
	}
	out := sb.String()
	for _, want := range []string{
		` ref="{urn:t}d1"`,
		`name="plain" form="unqualified"`,
		`<xs:sequence minOccurs="0" maxOccurs="unbounded">`,
		`<xs:any namespace="##other" minOccurs="0"/>`,
		"<xs:element name=\"inline\">",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("written model has no %s\n%s", want, out)
		}
	}
	m2, err := Load("written.xsd", []byte(out), nil)
	if err != nil {
		t.Fatalf("reload: %v\n%s", err, out)
	}
	body := `<root xmlns="urn:t"><plain xmlns=""/><inline><deep/></inline><zz xmlns="urn:other"/><d1/></root>`
	a, b := check(t, m, body), check(t, m2, body)
	if strings.Join(a, "|") != strings.Join(b, "|") {
		t.Errorf("verdicts differ after a round trip:\n  original %v\n  written  %v", a, b)
	}
	wantFindings(t, a, "element zz is not allowed here")
}

// ampSrc keeps an ampersand in the target namespace, which the writer has to
// escape in every attribute it appears in.
const ampSrc = `<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema"
    targetNamespace="urn:a&amp;b" xmlns:t="urn:a&amp;b" elementFormDefault="qualified">
  <xs:element name="root" type="t:R"/>
  <xs:complexType name="R"><xs:sequence><xs:element name="a" type="xs:string"/></xs:sequence></xs:complexType>
</xs:schema>`

// TestWriteEscapesAndSanitises covers attribute escaping and the comment that
// records the model's provenance.
func TestWriteEscapesAndSanitises(t *testing.T) {
	m := model(t, ampSrc)
	m.Source = "two -- dashes and a > angle"
	var sb strings.Builder
	if err := m.Write(&sb); err != nil {
		t.Fatal(err)
	}
	out := sb.String()
	if !strings.Contains(out, `targetNamespace="urn:a&amp;b"`) {
		t.Errorf("namespace was not escaped\n%s", out)
	}
	if strings.Contains(out, `"urn:a&b"`) {
		t.Error("an unescaped ampersand reached the output")
	}
	if !strings.Contains(out, "Derived from: two - dashes and a  angle") {
		t.Errorf("the provenance comment was not sanitised\n%s", out)
	}
	m2, err := Load("written.xsd", []byte(out), nil)
	if err != nil {
		t.Fatalf("reload: %v\n%s", err, out)
	}
	wantClean(t, check(t, m2, `<root xmlns="urn:a&amp;b"><a/></root>`))
}

// TestWriteRefusesDeepNesting guards the recursion limit that keeps a
// pathological content model from blowing the stack during generation.
func TestWriteRefusesDeepNesting(t *testing.T) {
	inner := &Term{Kind: Elem, Name: "x", Min: 1, Max: 1}
	for range 30 {
		inner = &Term{Kind: Seq, Min: 1, Max: 1, Parts: []*Term{inner}}
	}
	m := NewModel()
	m.Types["{urn:t}Deep"] = &Type{ID: "{urn:t}Deep", Term: inner}
	m.Decls["{urn:t}root"] = &Decl{ID: "{urn:t}root", NS: "urn:t", Type: m.Types["{urn:t}Deep"]}
	var sb strings.Builder
	if err := m.Write(&sb); err == nil || !strings.Contains(err.Error(), "nested too deeply") {
		t.Fatalf("want a depth error, got %v", err)
	}
}

// TestValidateNeedsARoot checks the message for a document with no element at
// all, which a browser-based tool sees whenever a user pastes an empty box.
func TestValidateNeedsARoot(t *testing.T) {
	m := model(t, seqSrc)
	got := m.Validate(&xp.Doc{})
	if len(got) != 1 || !strings.Contains(got[0].Message, "document has no root element") {
		t.Fatalf("want a no-root finding, got %v", got)
	}
}
