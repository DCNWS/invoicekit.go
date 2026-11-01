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
go get github.com/dcnws/invoicekit.go   # the library
go install github.com/dcnws/invoicekit.go/cmd/einv@latest   # the `einv` tool

Prebuilt static binaries for Linux, macOS and Windows are on the [releases](https://github.com/DCNWS/invoicekit.go/releases) page: one `CGO_ENABLED=0` file per platform, with `SHA256SUMS.txt` beside them, for anyone who wants the tool in CI without a Go toolchain.
```

Nothing to install to try it: the same engine, compiled to WebAssembly, runs
the free tools at [invoicekit.dcnws.com](https://invoicekit.dcnws.com) — it
validates a CII or UBL document, audits a Factur-X hybrid PDF, and generates
invoice XML. The [full EN 16931 rule
list](https://invoicekit.dcnws.com/en16931-rules.html) — 1,569 rule ids, 1,792
assertions, severity and syntax per rule — is the list this library enforces,
generated from the released engine. Every check runs in the browser; no
document is uploaded anywhere.

The module path ends in `.go` and the package is called `einvoice`, so an import
names one and the code uses the other:

```go
import einvoice "github.com/dcnws/invoicekit.go"

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

## Packages

Four packages are public API; everything under `internal/` is not, and may move
between releases without notice.

| import | what it is for |
|---|---|
| `github.com/dcnws/invoicekit.go` | the entry point: `Validate`, `Parse`, `Generate`, syntax detection, and phase, rule-set and rule listing |
| `github.com/dcnws/invoicekit.go/facturx` | write and read hybrid invoices — the PDF envelope, its attachment, its XMP — without touching the rule engine |
| `github.com/dcnws/invoicekit.go/hybrid` | audit a Factur-X / ZUGFeRD file the way a recipient's software does: envelope first, then the invoice inside it against a rule set |
| `github.com/dcnws/invoicekit.go/xsd` | XML Schema validity for both syntaxes, which the official Schematron sets never check |

`hybrid` is the one that joins the two halves: `facturx` sits below the rule
engine on purpose, so a PDF can be read without loading a single business rule,
and `hybrid` is where the PDF and the rule engine are allowed to meet.

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
einv rules [-json] [-syntax cii|ubl] [-phase ID] [-cius NAME] [-profile NAME]
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

`einv rules` answers the question a document cannot: which assertions does this
build enforce. One line per assertion — syntax, id, severity, the phases that
activate it, then the message as published, `[BR-21]-` prefix and all — so a rule
reference, a compliance register or a diff between two releases can be written
from the binary rather than from a PDF of the standard:

```
$ einv rules -syntax cii | wc -l
810

$ einv rules -syntax cii -phase syntax_phase | wc -l
587

$ einv rules -syntax cii -cius xrechnung | grep -m1 BR-DE-16
CII	BR-DE-16	fatal	xrechnung-model	[BR-DE-16] Wenn in einer Rechnung die Steuercodes S, Z, E, AE, K, G, L oder M verwendet werden, muss mindestens eines der Elemente "Seller VAT identifier" (BT-31)…
```

The base sets are the 810 CII and 982 UBL assertions the Commission publishes, and
`-cius` / `-profile` list what a run would add or replace — 862 assertions for an
XRechnung CII invoice, 1144 for a Peppol UBL one. `-json` adds each rule's XPath
context and test, which is what the public rule reference on the project site is
generated from: the page and the library are built from the same list, so they
cannot end up claiming different rule sets.

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

The WebAssembly entry point is `cmd/einv-wasm`, and `scripts/build-wasm.sh`
builds it with the JS glue it needs:

```
./scripts/build-wasm.sh        # -> wasm/einv.wasm (9.4 MB, 2.3 MB gzipped) + wasm_exec.js
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

## Speed

Measured here rather than asserted in prose: the official corpus — the 29
documents that ship with the CEF validation artefacts, 22 of them Peppol BIS
Billing 3.0 — through the public `Validate` call, one core, nothing cached
between iterations.

```sh
go test -run '^$' -bench . -benchmem -cpu 1 ./
```

```
cpu: AMD Ryzen 9 5950X (16 cores), one core under test — Go 1.27.3, linux/amd64, 2026-11-01
BenchmarkValidateUBL    228.8 ms/op   29 docs/op   7.9 ms per document   8.6 MB allocated per document
BenchmarkValidateCII      6.6 ms/op    1 doc/op    6.6 ms per document   6.9 MB allocated per document
```

One core is therefore worth roughly 125 documents a second, ten million a day,
and a document costs microseconds of a request rather than a server minute:
throughput is not what a per-document price is paying for. Allocation is the
number to watch if the engine is run in a tight loop — 8.6 MB per document is
the XPath evaluator building its node model, and it is a real cost in a
memory-limited process.

The WebAssembly build is the same engine, and Go's wasm port is slower than
native: about 31 ms a document in Node 26 with the module
`@invoicekit/einvoice` ships, which is still a few tens of milliseconds between
dropping a file and reading the verdict.

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
