# Notices and licences

## Code

The Go source of `github.com/dcnws/invoicekit.go` is licensed under the MIT
licence (see `LICENSE`).

## EN 16931 validation artefacts

`internal/en16931/schemas/en16931-cii.sch` and
`internal/en16931/schemas/en16931-ubl.sch` are the official preprocessed
EN 16931 Schematron rule sets (validation artefacts 1.3.x) published by the
European Union through the Connecting Europe Facility:

* Source: <https://github.com/ConnectingEurope/eInvoicing-EN16931>
* Licence: European Union Public Licence v1.2 — full text in
  `LICENSES/EUPL-1.2.txt`

They are redistributed **unmodified**, with their notices intact. They are
embedded into the compiled binary with `go:embed` purely so the validator works
offline with no downloads; the rule sets are data, not part of the Go code, and
remain under the EUPL. If you redistribute this project, keep them unmodified
and keep this notice.

## XRechnung CIUS rule sets

`internal/en16931/schemas/cius/xrechnung/` holds the official XRechnung
Schematron rule sets — `XRechnung-CII-validation.sch`,
`XRechnung-UBL-validation.sch` and the `common.sch` they include — version
**2.6.0**, written for **XRechnung 3.0.2**, published by the KoSIT (the
coordinate IT office of the German federal administration) on behalf of the
Bundesministerium des Innern:

* Source: <https://projekte.kosit.org/xrechnung/xrechnung-schematron>, tag `v2.6.0`
* Licence: Apache License 2.0 — full text in `LICENSES/Apache-2.0.txt`

They are redistributed **unmodified**; the files in this repository are
byte-identical to that tag, and the `<title>` placeholders inside them
(`@xr-schematron.version.full@`) are upstream's own, filled in by its build and
left alone by us. They are embedded with `go:embed` so the validator works
offline, and they stay Apache-2.0 licensed data, not part of our Go code. If you
redistribute this project, keep them unmodified and keep this notice.

The rule text is German, because that is the language the rules were written in;
we do not translate findings.

## Peppol BIS Billing 3.0 rule sets

`internal/en16931/schemas/peppol/` holds the rule sets OpenPEPPOL publishes for
Peppol BIS Billing 3.0, release **3.0.21** (May 2026):

* `CEN-EN16931-CII.sch`, `CEN-EN16931-UBL.sch` — the EN 16931 Schematrons (artefacts
  1.3.16) as the profile republishes them, credited in reports as `peppol-cen`
* `PEPPOL-EN16931-CII.sch`, `PEPPOL-EN16931-UBL.sch` — the network's own rules, and
  the DE, DK, GR, IS, IT, NL, NO and SE rules carried in the same files, credited as
  `peppol`

* Source: <https://github.com/OpenPeppol/peppol-bis-invoice-3> (`rules/sch`)
* Copyright: OpenPeppol AISBL for the Peppol BIS; CEN and its members for
  EN 16931:2017 itself
* Notice: full statement in `LICENSES/Peppol-BIS-3.0-notice.txt`

That repository publishes no licence file. It does carry a statement of
copyright: OpenPeppol AISBL holds the copyright of the Peppol BIS, "developed and
published subject to the conditions of the agreement between the European
Commission and CEN allowing derivate works to be developed and distributed with
no additional licensing requirements", while the copyright of EN 16931:2017
stays with CEN and its members, who provide EN 16931-1 free of charge. The
artefacts themselves say the CEN business terms are "reproduced with permission
from CEN".

We redistribute the four files **unmodified** — gzip is the only transformation,
and `TestProfileArtefactsUnmodified` fails the build the moment an embedded
artefact drifts from the released one — and we keep upstream's headers intact.
No CEN licence text is shipped, because none is published with these artefacts.
Peppol BIS Billing 3.0 is a trademark of OpenPEPPOL; the rules are used to check
documents, not to imply anything about who stands behind this software. If you
redistribute this project, keep those files unmodified and keep this notice.

## No warranty

The rule sets and this validator are provided as-is. Nothing here is legal or
tax advice; compliance with your national e-invoicing mandate remains your
responsibility.

## Test fixtures

`testdata/samples/cii-xrechnung.xml` and `testdata/samples/ubl-min.xml` are two
of the CEF reference examples — `XRechnung-O.xml` and
`Invoice-Min_content_with_VAT.xml` from
<https://github.com/ConnectingEurope/eInvoicing-EN16931>, EUPL v1.2 — kept
unmodified as sample documents for the CLI, the pages and the test suites.
`testdata/samples/ubl-peppol.xml` is ours: an example invoice written to pass the
Peppol BIS Billing 3.0 rules, which the website offers as its Peppol sample.

`testdata/invoice-pdfa3.pdf` and `testdata/invoice-plain.pdf` are one-page
invoice pages rendered by GPL Ghostscript from `testdata/invoice-page.ps`,
which is ours. The fonts Ghostscript embedded in them are the URW base 35 faces
(Underware licence, GPL-compatible); the colour profile is one of Ghostscript's
own. They are test data, not part of the library, and they are kept
unmodified so the reader is tested against files a real producer wrote. The
static site serves both of them, with `invoice-model.json`, from `samples/` as
starting points for the generator, so this notice ships alongside them.

`js/test/fixtures/hybrid-a3.pdf` and `js/test/fixtures/hybrid-plain.pdf` are
those two files with an invoice attached by our own `einv embed`, using the
sample in `testdata/samples/cii-xrechnung.xml`. They are reproducible:

```
go run ./cmd/einv embed -now 2026-10-31T09:00:00Z \
  -o js/test/fixtures/hybrid-a3.pdf testdata/invoice-pdfa3.pdf testdata/samples/cii-xrechnung.xml
```

The `-now` flag pins the timestamps, so rebuilding them byte-for-byte is a
matter of re-running the command, and the fonts and profile inside them are the
same URW faces and Ghostscript profile as their sources.
