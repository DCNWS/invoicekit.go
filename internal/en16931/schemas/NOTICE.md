The two Schematron files in this directory are the official, preprocessed
EN 16931 validation rule sets published by the European Commission (Connecting
Europe Facility, CEF):

  EN16931-CII-validation-preprocessed.sch   (CI/UN-CEFACT syntax)
  EN16931-UBL-validation-preprocessed.sch   (OASIS UBL syntax)

Source: https://github.com/ConnectingEurope/eInvoicing-EN16931
Licence: European Union Public Licence (EUPL) version 1.2.
Copyright: European Union.

They are redistributed here unmodified so that the validator can embed them.
See https://joinup.ec.europa.eu/collection/eupl for the licence text.

The two .xml.gz files are validity models derived from the official XML schemas
of the same two syntaxes: the UN/CEFACT CrossIndustryInvoice 100 D16B schema
(EUPL 1.2, via the CEF repository above) and the OASIS UBL 2.1 schemas (OASIS
Open). They are not the schema documents themselves - cmd/xsdgen keeps only the
components reachable from the invoice and credit note roots and writes that
subset out as one self-contained schema, gzipped: 3 MB of schema becomes 29 KB.
Element names, namespaces, cardinalities and order are carried across verbatim,
so what the official schemas accept, this accepts.

The source schema documents are not vendored here - they are 3 MB of mostly
documentation, and the model above is what the library reads. To regenerate
them from scratch, unpack the two sets and run cmd/xsdgen:

  # UN/CEFACT CII: "D16B SCRDM (Subset)/uncoupled clm/CII/uncefact/data/standard/"
  # from https://github.com/ConnectingEurope/eInvoicing-EN16931 (EUPL 1.2)
  go run ./cmd/xsdgen -dir <cii directory> \
      -schema CrossIndustryInvoice_100pD16B.xsd \
      -root '{urn:un:unece:uncefact:data:standard:CrossIndustryInvoice:100}CrossIndustryInvoice' \
      -out internal/en16931/schemas/cii-xsd.xml.gz

  # OASIS UBL 2.1: http://docs.oasis-open.org/ubl/os-UBL-2.1/UBL-2.1.zip
  go run ./cmd/xsdgen -dir <ubl directory> \
      -schema UBL-Invoice-2.1.xsd \
      -schema UBL-CreditNote-2.1.xsd \
      -root '{urn:oasis:names:specification:ubl:schema:xsd:Invoice-2}Invoice' \
      -root '{urn:oasis:names:specification:ubl:schema:xsd:CreditNote-2}CreditNote' \
      -out internal/en16931/schemas/ubl-xsd.xml.gz

Both are reproducible: re-running them writes the checked-in bytes.

The four .sch.gz files under schemas/peppol/ are OpenPEPPOL's Peppol BIS Billing
3.0 rule sets (release 3.0.21, May 2026), taken from
https://github.com/OpenPeppol/peppol-bis-invoice-3 (rules/sch) and redistributed
unmodified - gzip is the only change. Two of them are CEN's EN 16931 Schematrons
as the profile republishes them, two are OpenPEPPOL's own. Upstream publishes no
licence file; its statement of copyright is quoted in
LICENSES/Peppol-BIS-3.0-notice.txt at the repository root and summarised in the
NOTICE.md there. A test compares the embedded bytes with the released files, so
a modified artefact fails the build rather than quietly changing verdicts.
