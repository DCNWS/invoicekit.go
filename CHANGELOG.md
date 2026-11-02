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
