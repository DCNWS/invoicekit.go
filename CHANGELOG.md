# Changelog

What changed in each release of this library, the `einv` command and the GitHub
Action. Published because a validator that will not say what it changed is a
validator nobody can cite: every entry states the rule sets the release embeds,
so a finding can be reproduced a year later.

The rule sets in **every release to date** are EN 16931 Schematron v1.3.17
(last update 2026-10-09, CEN/TS and the European Commission artefacts),
XRechnung CIUS 2.6.0 (XRechnung 3.0.2) and Peppol BIS Billing 3.0 release
3.0.21. No release so far has moved a rule set, so no release so far has
changed what is valid. `einvoice.Artefacts()` reports these at runtime.

## v0.2.12 — 2026-11-10

Rule sets unchanged: EN 16931 Schematron v1.3.17, XRechnung CIUS 2.6.0
(XRechnung 3.0.2) and Peppol BIS Billing 3.0 release 3.0.21. Nothing in this
release changes what is valid; it changes what the checker wrongly complains
about, and what a generated document is made of.

- **Fixed:** `xs:any` was read with its occurrence counts left at zero, so a
  UBL document's extension content was reported as "not allowed here".
  `ExtensionContentType` is a sequence holding exactly one `xs:any`, and that
  is where national and community profiles put their business-process
  content — so invoices that every other validator accepted were rejected
  outright. A wildcard now keeps its `minOccurs`/`maxOccurs`, and matching it
  no longer descends into its subtree and complains about a schema the
  document is not using. The embedded UBL model has been regenerated; the
  only change in it is the wildcard gaining those counts.
- **Fixed:** a Schematron that failed to compile reported its errors once and
  then returned a clean verdict to anyone who called it twice. Compile keeps
  reporting the collected errors and Validate refuses such a schema, so a
  broken rule set can never produce a pass.
- **Fixed:** XPath node tests. `text()`, `comment()` and
  `processing-instruction()` were flagged as wild name tests because they do
  not constrain a name, so they matched every element and attribute they were
  tested against; `element(ram:A)` and `attribute(id)` carried the same flag,
  so a named kind test matched the whole namespace. A child step `prefix:*`
  resolved its prefix through a path that returned an empty namespace for an
  undeclared prefix, which let a mistyped prefix match everything.
- **Fixed:** node identity for attributes and element text. A step built a
  fresh node each time it ran, so `@id | @id` counted the same attribute
  twice and `a[1]/text() is a[1]/text()` was false; `parent::` from a virtual
  node started one level too high, `name()` dropped the prefix,
  `prefix-from-QName()` returned the local name, and `namespace-uri()` was not
  a builtin at all. An empty document panicked on the root step rather than
  having no root element.
- **Fixed:** a missing operand — `1 +`, an empty predicate, an absent call
  argument — compiled into an expression with a nil child. Every expression
  now passes through one place that can tell "the operand is missing" from
  "the operand has ended", and a bare `/` or `//`, which is legal XPath and
  common in hand-written Schematron, selects the document node and its
  descendants instead of failing to parse.
- **Fixed:** the PDF filters. `RunLengthDecode` copied one byte too few for a
  literal run and one too few for a repeat run, so a filtered stream came back
  short by a byte per run. `ASCII85Decode` padded a group cut short by the
  writer with zeros, which rounds the last recovered byte down; the unknown
  digits are now the largest digit of the alphabet, with a 64-bit accumulator,
  since a padded group can pass 2^32.
- **Fixed:** the ICC profile generated for a hybrid PDF. Reading it back with a
  colour engine found six defects: the chromatic adaptation multiplied the
  inverse Bradford transform by the cone response of the source white instead
  of the destination's, the header's PCS illuminant ran into the creator field,
  a v4 output profile has to carry `chad` and there was none, `desc` wrote an
  ASCII count over the reserved field and started its text four bytes early,
  `cprt` gave a length prefix to a `textType`, which has none, and `gTRC` was
  missing from the tag table while the tag data was not aligned to the
  4-byte boundary ICC asks for. The primaries now match the published sRGB
  D50 values, their columns sum to the profile's white point, and Ghostscript
  renders a hybrid PDF carrying the profile without complaint.
- **Added:** a coverage pass over the XPath engine, the XSD reader, the PDF
  writer and filters, the hybrid auditor and the command functions, including
  the error paths that only a malformed document can provoke.

## v0.2.11 — 2026-11-03

- **Fixed:** a fiscal number is no longer written as a VAT number. UBL
  names the register on the tax scheme element around a registration
  number: `VAT` holds BT-31 and BT-48, `TAX` holds BT-32, which CII
  labels `FC`. v0.2.10 put every number under `VAT` and labelled the
  identifier with `@schemeID` instead, so a German invoice's
  Steuernummer crossed into UBL as a VAT number — and UBL's rules read
  that scheme, not the label: BR-CO-09 then wants an ISO country prefix
  in the number, BR-O-02 and BR-O-04 reject the invoice outright
  whenever a line is not subject to VAT, and UBL-CR-652 warns away the
  label it carried. A VAT number and a fiscal number now sit side by
  side (UBL-SR-42 gives a party two tax scheme elements); a third is
  still reported rather than chosen between, and a reader takes the
  basis back from the scheme — `TAX` reads as `FC` — so both directions
  round-trip.
- **Fixed:** a VAT breakdown states its rate (BT-119) under category O
  as well. BR-48 excuses the rate only for a document with no VAT at
  all, and XRechnung asks for it in every category (BR-DE-14), so
  leaving it out turned a valid CII XRechnung into an invalid UBL one.
  The reason once given for skipping it was a misreading of UBL-CR-499,
  which warns away a percent written on the subtotal itself, not one in
  the category beside it.
- **Added:** `TestConversionIntroducesNoNewBreach` converts a real CII
  XRechnung to UBL and a real Peppol UBL invoice to CII and compares the
  finding sets. A conversion may neither introduce a finding nor lose
  one: either way the verdict moves somewhere the sender never put it.

## v0.2.10 — 2026-11-03

- **Fixed:** a tax registration kept the register it came from. CII says
  which one a number is: `VA` is the VAT number and `FC` the fiscal
  number a German profile writes where it has no VAT number. The reader
  dropped that label and called every number a VAT number, so the
  writer filed anything else under `cac:PartyLegalEntity/cbc:CompanyID`
  — the company register, BT-30, which is not a tax register at all.
  The basis now rides on `TaxID.Scheme`, is written as the identifier's
  `@schemeID` inside `cac:PartyTaxScheme`, and is read back from there.
  UBL has no term for a non-VAT tax registration, so such a number goes
  where the VAT number goes and the document is reported under the rules
  that follow from it (BR-O-02, BR-O-04, BR-CO-09) instead of being made
  to look valid by losing the number.
- **Fixed:** the legal registration identifier (BT-30, BT-47) keeps its
  `@schemeID` — an ICD code, `0189` being the German commercial register
  — through both readers and both writers. It used to survive as a bare
  string, which says nothing about which register issued it.
- **Fixed:** an allowance or charge keeps the base its percentage was
  taken on (BT-93, BT-100, BT-137, BT-142): `AllowanceCharge` has a
  `Basis` field. A reader used to throw the base away, and keep either
  the stated amount or the percentage, so a document whose amount
  disagreed with its percentage came back with the amount replaced by
  the engine's own arithmetic. Every stated figure is kept, the
  disagreement is still reported, and a writer measures the percentage
  against the document's base, falling back to the line net or the
  invoice total only where the document stated no base. A percentage
  taken on a different base is different money.
- **Added:** a finding when a UBL bank account would have to hold two
  identifiers, an IBAN and a private number: EN 16931 gives an account
  one, so the second is dropped, and a drop nobody is told about is a
  conversion that quietly loses the address money is due at.

No rule set moved, and the verdicts on the three sample documents are
unchanged — 7 fatal and 6 warnings under XRechnung — so the Action's pin
can follow this release without moving the counts it asserts.

## v0.2.9 — 2026-11-02

- **Fixed:** the GitHub Action cited the wrong rule book. It asked the binary
  which artefacts it carries *without* repeating the `-cius`/`-profile` it had
  just validated under, so a run against XRechnung printed
  `rule_sets=CII 1.3.17, UBL 1.3.17` and left out the CIUS that produced most
  of the findings — a receipt for a verdict that was not the one handed down.
  The selectors are now built once and both commands read them.
- **Added:** unit tests for the Action's reporter (`node --test action/`), run
  in CI: the exit code for each `fail-on`, the annotation budget, and the
  citation string, including the CIUS case the self-check had to catch by
  failing. An action with no offline test is an action tested only by red
  builds.

## v0.2.8 — 2026-11-02

- **Added:** `einv artefacts [-json] [-cius NAME] [-profile NAME]` prints the
  engine release and the version and last-update date of every embedded rule
  set — the other half of a verdict, available to anyone holding nothing but a
  binary.
- **Added:** the GitHub Action cites the same information: an `engine` and a
  `rule_sets` output, and a line in the job summary naming the artefact releases
  a run was judged against. A red build now says what it was red under.

## v0.2.7 — 2026-11-02

- **Added:** `einvoice.Artefacts()` returns the release and last-update date of
  the embedded rule set for each syntax. This is the answer to "which artefacts
  produced this verdict?" without reading the source or guessing from a tag.
- No behaviour change: the rule sets, the rule ids and the findings are exactly
  what v0.2.6 reported.

## v0.2.6 — 2026-11-02

- **Added:** the repository is a GitHub Action. `uses: DCNWS/invoicekit.go@v0.2.6`
  validates the invoice XML a pull request touches on the runner, downloads the
  pinned release binary, verifies it against `SHA256SUMS.txt`, and turns each
  finding into an annotation on the offending file. Inputs: `files`, `cius`,
  `profile`, `version`, `fail-on: fatal|any|never`; outputs: `files`, `fatal`,
  `warn`.
- **Fixed:** a release with findings reported as a crashed step rather than as
  findings. The gate is checked in both directions in CI — a document with fatal
  findings must fail, a document with warnings only must pass.
- Coverage tooling: targeted insertion mutants for assertions nothing else
  fires, and a sweep whose order no longer affects the corpus it writes.

## v0.2.5 — 2026-11-02

- **Performance:** predicate sequences stay typed instead of passing through an
  interface. Measured effect on the official CEF corpus is in `bench_test.go`.

## v0.2.4 — 2026-11-01

- **Performance:** the XPath builtin table lookup stops costing allocation per
  call. No behaviour change.

## v0.2.3 — 2026-11-01

- **Fixed:** `einv validate -json` failed silently on an unreadable document
  instead of reporting it and exiting 2.
- **Fixed:** the PDF predictor decoder chose the wrong Paeth neighbour, which
  corrupted some Factur-X attachments.
- Internal: the XPath engine's lexer, step handling and builtin dispatch are split
  into single-purpose functions, and the lint configuration the tree is held to is
  committed. No API change.

## v0.2.2 — 2026-11-01

- **Performance:** validating one document became roughly three times cheaper by
  sharing one parsed schema model safely across calls.
- **Added:** static `CGO_ENABLED=0` binaries of `einv` on the release page, one per
  platform (Linux and macOS on amd64 and arm64, Windows amd64), with
  `SHA256SUMS.txt` beside them, for CI images with no Go toolchain.
- **Added:** in-repository throughput benchmarks, so every speed figure quoted in
  the README has a command that reproduces it.

## v0.2.1 — 2026-11-01

- **Fixed:** a release tag could disagree with the version the binaries reported.
  v0.2.0 shipped a `go.mod` that said 0.2.0 while the embedded engine introduced
  itself as 0.1.0. CI now fails when a tag and `einvoice.Version` disagree.

## v0.2.0 — 2026-11-01

- **Added:** `einvoice.Rules` and `einv rules` list the rules a build enforces,
  with the published requirement text and test behind each id.
- **Added:** `xsdgen`, the generator used to build the embedded schemas.

## v0.1.0 — 2026-11-01

First release. EN 16931 validation of CII and UBL XML (schema and business
rules), Factur-X and ZUGFeRD PDF auditing, CII↔UBL conversion, and invoice
generation from a Go model — pure Go, no CGO, no Java, no network calls. The
`einv` command and a WebAssembly build of the same code.
