# invoicekit.go — EN 16931 invoice validation in pure Go

Validate **EN 16931** electronic invoices — UN/CEFACT CII (Factur-X, ZUGFeRD,
XRechnung) and OASIS UBL (Peppol BIS) — for two separate things at once: the
structure allowed by the official XML schema of that syntax, and the business
rules of the official European Commission Schematron. On top of the norm sit
**profiles**: Peppol BIS Billing 3.0, and national CIUS rule sets such as
Germany's XRechnung.

No CGO, no XSLT, no Java, no `xmllint`, no network calls: a small XPath 2.0
evaluator and Schematron runner written from scratch in Go, with the official
rule sets embedded.

```
go get github.com/dcnws/invoicekit.go
```

The same engine ships as the `einv` command line tool in this repository and as
WebAssembly in [`invoicekit.ts`](https://github.com/dcnws/invoicekit.ts), so a Go
service, a CI pipeline and a browser tab all give the same verdict from the same
code. See [Command line](#command-line).

```go
res, err := einvoice.Validate(data, einvoice.Options{})   // syntax auto-detected
if err != nil {
    return err
}
if !res.Valid {
    for _, f := range res.Findings {
        log.Printf("%s: %s (at %s)", f.ID, f.Message, f.Context)
    }
    for _, f := range res.Schema {
        log.Printf("structure: %s (at %s)", f.Message, f.Location)
    }
}
```

## Structure as well as business rules

A released invoice can satisfy every EN 16931 rule and still be rejected by a
recipient's parser, because the published Schematron checks presence and counts,
not shape: of 587 CII syntax assertions, not one says which element may follow
another. Swapping two amounts, or putting an element where its neighbour
belongs, is invisible to it.

```
Invoice/AllowanceCharge: BaseAmount before Amount
  Schematron: 0 fatal, 0 warning
  schema:     element BaseAmount is not allowed here; expected Amount
```

The schema checker in this module answers that second question. It reads an XML schema, compiles
each content model into a small matcher, and reports elements that are unknown,
out of order, repeated past their maximum, or missing when mandatory. The models
are the official CII and UBL schemas pruned to what an invoice can actually
contain — 3 MB of XSD becomes 29 KB gzipped, loaded once and cached — so the
check adds no dependency and no network call, and all 61 official instances
still pass it in half a second.
## Command line

```
einv validate [-json] [-max N] [-syntax cii|ubl] [-phases a,b]
              [-cius NAME] [-profile NAME] <file|->...
einv gen [-syntax cii|ubl] [-o out] [-cius NAME] [-profile NAME] <file|->
einv parse [-o out] <file|->
einv detect <file>
einv phases [cii|ubl]
einv cius [name]
einv profile [name]
einv embed [flags] <pdf> <xml>
einv extract [-o out.xml] [-info] <pdf>
einv check [-json] [-max N] [-cius NAME] [-profile NAME] <file|->...
```

`einv validate` exits `0` when every document is valid, `1` when at least one
document breaks a rule, and `2` on usage or I/O errors — so it drops straight
into CI:

```
einv validate dist/invoices/*.xml
```

```
$ einv validate invoice.xml
invoice.xml: INVALID UBL invoice (2 fatal, 0 warning)
  BR-21        [BR-21]-Each Invoice line (BG-25) shall have an Invoice line identifier (BT-126). at cac:InvoiceLine | cac:CreditNoteLine
```

The last three commands deal with hybrid PDFs — an invoice PDF that carries its
own XML inside it, which is what Factur-X and Order-X are. They need no external
tools: `einv embed` parses the PDF and rewrites it with the XML attachment, the
XMP metadata and an output intent, and `einv extract` reads the XML back out.
`einv check` audits a hybrid that is already on disk and reports what a
conformance validator would make of it, which is the quickest way to find out
whether an invoice you receive will pass. `embed` refuses input that already
carries the payload attachment rather than stacking a second invoice into one
file, and the audit reports a file that declares more than one XML attachment.

```
$ einv check invoice-hybrid.pdf
invoice-hybrid.pdf: Factur-X EN 16931 envelope, payload factur-x.xml (XML says Factur-X EN 16931), 1 page(s)
  info    pdfa.tagged            document is not tagged: fine for PDF/A-3b and 3u, but it can never claim level A
  error   pdfa.font              font "Helvetica" is not embedded: PDF/A forbids the standard 14 fonts unless a copy travels with the file

$ einv check -cius xrechnung invoice-hybrid.pdf
invoice-hybrid.pdf: Factur-X EN 16931 envelope, payload factur-x.xml (XML says Factur-X EN 16931), 1 page(s)
  info    pdfa.tagged            document is not tagged: fine for PDF/A-3b and 3u, but it can never claim level A
  invoice inside: INVALID CII + xrechnung invoice (0 fatal, 3 warning, 0 structural)
    BR-DE-21     [BR-DE-21] Das Element "Specification identifier" (BT-24) soll syntaktisch der Kennung des Standards XRechnung entsprechen. at /rsm:CrossIndustryInvoice/rsm:ExchangedDocumentContext
    BR-DE-27     [BR-DE-27] In BT-42 sollen mindestens drei Ziffern enthalten sein. at …/ram:SellerTradeParty/ram:DefinedTradeContact
    BR-DE-28     [BR-DE-28] In BT-43 soll genau ein @-Zeichen enthalten sein … at …/ram:SellerTradeParty/ram:DefinedTradeContact
```

An embedded font, an output intent and catalog-referenced XMP are what stand
between an ordinary hybrid PDF and a PDF/A claim, so `einv check` reports them by
name rather than as a verdict you have to go and decode.

`einv check` then runs the business rules over the XML inside, in the same pass
over the file, and takes the same `-cius`, `-profile` and `-max` options as
`einv validate`. The two verdicts are reported separately on purpose: a
conformance validator accepts the envelope while the invoice in it breaks a rule,
and fixing the PDF would not fix that. A hybrid with no XML attached gets an
envelope verdict and nothing else; XML that is not an invoice becomes one
`fx.rules` finding rather than a rule report that never ran.

Which PDF/A a hybrid claims is part of the report too, because the part decides
what an embedded file means: PDF/A-1 forbids attachments, so a part-1 document is
either a validator rejection or an older ZUGFeRD file whose XML travels beside it
as a companion `.xml` — `einv check` tells you which one you are holding.

`einv gen` turns a JSON invoice model into an invoice. Ask it for `-cius` or
`-profile` and it renders the things that rule set wants filled in — a Peppol
document carries a `PEPPOL` specification identifier and an endpoint scheme, an
XRechnung one carries `XRechnung` — and then validates its own output, so a model
that cannot satisfy the profile fails there instead of at a customer's inbox.
`testdata/*-model.json` holds worked examples of all three shapes: the plain
EN 16931 invoice, a Peppol BIS one, and a un-CEFACT XRechnung one.

`einv parse` is `gen` seen from the other end. It reads an invoice or credit note
in either syntax back into that same JSON model, so a document a customer sends
can be inspected, edited and re-rendered instead of parsed by hand. Whatever the
document says that the model has no room for is named on stderr — a tax category
the norm does not carry, an element from a schema this library does not read, a
second copy of something the model holds once — and the invoice comes back
anyway, because a model that says what it lost is better than one that quietly
lost it three systems down.

## Profiles: Peppol BIS and national CIUS

EN 16931 lets a network or a country tighten the norm without relaxing it. Two
options say which rules a document is checked against, and they behave
differently. `Options.CIUS` layers a national rule set on top of the EN 16931
artefacts we ship. `Options.Profile` swaps in a published bundle that carries its
own copy of the EN 16931 assertions, so the verdict — ids, wording and artefact
versions included — is the one that network's own validator would reach. A name
that is not embedded is a hard error naming what is, never a quietly skipped
profile.

```go
res, err := einvoice.Validate(data, einvoice.Options{Profile: einvoice.ProfilePeppol})
// the CEN EN 16931 artefacts, the PEPPOL-EN16931-* and PEPPOL-COMMON-* rules,
// and the country rules that travel with them (DE-, DK-, GR-, IT-, NL-, NO-, SE-)

res, err := einvoice.Validate(data, einvoice.Options{CIUS: einvoice.CIUSXRechnung})
// findings now include BR-DE-*, BR-DE-CVD-*, BR-DEX-* and BR-TMP-*
```

```
$ einv profile
peppol    Peppol BIS Billing 3.0 release 3.0.21 (May 2026), with EN 16931 Schematron 1.3.16

$ einv validate -cius xrechnung cii-br-de-25-test-bg-19-with-bg-18.xml
cii-br-de-25-test-bg-19-with-bg-18.xml: INVALID CII + xrechnung invoice (1 fatal, 1 warning, 0 structural)
  BR-DE-25-b   [BR-DE-25-b] Wenn BT-81 "Payment means type code" einen Schlüssel für
               Lastschriften enthält (59), dürfen BG-17 und BG-18 nicht übermittelt werden.
               at …/ram:SpecifiedTradeSettlementPaymentMeans[normalize-space(ram:TypeCode) = '59']

$ einv validate -profile peppol ubl-min.xml
ubl-min.xml: INVALID UBL peppol invoice (2 fatal, 0 warning, 0 structural)
  PEPPOL-COMMON-R049 [PEPPOL-COMMON-R049]-Swedish organization number MUST be stated in
               the correct format. at cbc:EndpointID[@schemeID = '0007'] | cac:PartyIdentification/cbc:ID[@schemeID = '0007'] | …
```

`einv profile peppol` prints the phases of that bundle the way `einv phases`
prints EN 16931's, and rule text arrives in the upstream's own language and
wording: the finding is the rule the Germans wrote, or the rule OpenPEPPOL wrote,
not a paraphrase of it.

Measured against our own mutation corpus — 148 documents, each built to break
exactly one rule — the Peppol bundle fires every rule id our artefacts fire, the
same number of times, plus 32 ids of its own. One gap is worth knowing: CEN's
copy of the norm holds 806 CII and 979 UBL assertions where the Commission
publishes 810 and 982, so seven ids (CII-DT-105, CII-DT-106, CII-SR-497,
UBL-SR-25, UBL-SR-57, UBL-SR-58, UBL-SR-59) stay quiet under the bundle. Ask the
profile for the network's answer; ask the base for the norm's.

XRechnung is the only CIUS embedded so far, because it is the one our own
invoices have to pass. French Factur-X does not add Schematron rules beyond
EN 16931, so there is nothing to layer there — what France adds is the hybrid PDF
envelope, which is what `einv embed` and `einv check` are for.

## TypeScript / browser

The same engine compiles to WebAssembly, so validation can run client-side with
no server and no upload of invoice data:

```ts
import { loadEinvoice } from '@invoicekit/einvoice';

const einvoice = await loadEinvoice();   // 7.7 MB of wasm, 2.0 MB gzipped
const res = einvoice.validate(xml);      // { valid, findings, fatal, warn }
const de = einvoice.validate(xml, { cius: "xrechnung" });  // EN 16931 + BR-DE rules
const bis = einvoice.validate(xml, { profile: "peppol" }); // Peppol BIS Billing 3.0
const cius = einvoice.cius();            // [{ name, version, phases: { CII, UBL } }]
const bundles = einvoice.profiles();     // [{ name, version, phases: { CII, UBL } }]

const audit = einvoice.inspect(pdfBytes);   // Factur-X envelope: { ok, findings, envelope }
const doc = einvoice.extract(pdfBytes);     // { name, envelope, xml } from inside the PDF
```

The PDF parser compiles too, so a hybrid invoice can be audited and taken apart
client-side — no upload of the PDF pages either, which is where the client name
and bank details are.

The WebAssembly entry point is `cmd/einv-wasm`: build it and the TypeScript
package wraps it.

```
GOOS=js GOARCH=wasm go build -o einv.wasm ./cmd/einv-wasm
```

The TypeScript package, its tests and its npm packaging live in
[`invoicekit.ts`](https://github.com/dcnws/invoicekit.ts).

## Why

Most EN 16931 validation available to developers is either a Java/Saxon XSLT
pipeline wrapped in a shell script, or a hosted endpoint that quietly uploads
your customers' invoices somewhere else. Both are awkward in a Go service and a
privacy headache for invoices, which are full of personal data. This is a
library you link, auditable, offline and deterministic.

## Conformance

The validator is tested against the official artefacts:

* the **29** full CEF/TC434 UBL instances and **15** CII examples (including
  XRechnung) validate with **zero** findings;
* **1140** VEFA unit test cases from the official test sets: every one of the
  **568** "must not fire" assertions stays silent and every one of the **572**
  "must fire" assertions fires;
* a harvested corpus of **148** mutants — real invoices broken on purpose
  (deleted invoice number, missing seller or buyer party, no invoice lines,
  totals that disagree with the line sums, amounts holding `zz` or a
  `2001-13-01` date, a VAT rate that disagrees with its taxable amount) —
  replays as **291** expected rule firings with no misses.

Coverage is measured assert by assert rather than test by test: **171** of the
**810** active CII asserts and **271** of the **982** active UBL asserts have a
document that makes them fire. The gap sits in the structural syntax patterns
(`EN16931-CII-Syntax` 44/587, `UBL-syntax` 65/759), which largely restate XSD
content models; the model and codelist patterns are nearly complete (CII 105/200
and 22/23, UBL 184/201 and 22/22). `go run ./cmd/cov` re-runs the mutation sweep,
replays the corpus and regenerates those numbers.

Phase selection follows ISO Schematron: naming phases restricts the rules that
run, and with no phase selected every pattern is active — including the UBL
pattern that holds the `UBL-SR-*` structural rules and is referenced by no
`<phase>` in the published schema.

## Status

`v0.1` — the business rules and the schema validity of both mandatory syntaxes
(CII and UBL), phase selection, syntax detection, invoice generation from a
neutral model, Factur-X / Order-X hybrid PDFs — writing them, reading them back
and auditing one that is already on disk, with a PDF parser and writer that pull
in no external tools — the German XRechnung CIUS rules, and the whole Peppol BIS
Billing 3.0 bundle, country rules included — reachable as a library and as a CLI,
and compiled to WebAssembly for the browser. Not yet implemented: CIUS rule sets
beyond XRechnung, and CII↔UBL conversion.

The hybrid commands say when a PDF cannot carry a PDF/A claim — a standard-14
font with no embedded copy, no output intent, XMP sitting in the trailer instead
of the catalog — but they will not fix it for you. Subsetting and embedding a
font is work for whatever drew the page.

## Running the tests

```
go test ./...
```

Everything needed to pass is in the repository: the rule sets, the 44 reference
instances, and the harvested mutation corpus. Three suites exercise extra
upstream corpora that cannot be redistributed here, and skip when they are
absent, so a plain `go test ./...` never fails over a download nobody did:

| env var | corpus | what it proves |
|---|---|---|
| `EINVOICE_REF` | [CEF eInvoicing-EN16931](https://github.com/ConnectingEurope/eInvoicing-EN16931) | 1,140 official VEFA cases, 148 full instances |
| `EINVOICE_PEPPOL` | Peppol BIS 3.0 release artefacts | the network's own rule bundle |
| `EINVOICE_XRECHNUNG` | KoSIT XRechnung Schematron | the national profile as published |

Each is also picked up automatically as `../ref-en16931`, `../ref-peppol` or
`../ref-xrechnung` beside this checkout. `go run ./cmd/cov` re-runs the mutation
sweep that the coverage figures above come from.

## Licence

Code: MIT. The embedded rule sets are somebody else's copyright and are
redistributed unmodified: the EN 16931 Schematrons belong to the European Union
(EUPL v1.2), the XRechnung sets to the German coordinate IT office (KoSIT,
Apache-2.0), and the Peppol BIS bundle to Peppol AISBL, whose rules are CEN
EN 16931 business terms (European Union, EUPL v1.2) plus the OpenPEPPOL
extensions (Apache-2.0). See `NOTICE.md` and `LICENSES/`.
