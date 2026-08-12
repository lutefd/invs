# ADR 0003: Versioned canonical domain models

- Status: Accepted
- Date: 2026-08-12

## Context

SEC, B3, CVM, macro agencies, and price vendors use incompatible identifiers and
payload shapes. Vendor fields cannot be allowed to become the research API.

## Decision

The JSON Schemas in `/schemas` are the language-neutral canonical contract. They use
JSON Schema 2020-12, carry an explicit `schema_version`, reject unknown fields, use
UUID internal identities, and represent lossless decimal values as strings. Go and
Python models are generated from or tested against these contracts.

The core entities are:

- **Issuer**: a legal or reporting organization. It owns issuer-level facts and
  filings, and may issue several securities.
- **Security**: a tradable instrument with its own UUID, listing metadata, currency,
  and optional issuer. Tickers are attributes resolved through versioned identifiers,
  never primary keys.
- **SecurityIdentifier**: a typed, scoped value valid over a half-open interval
  `[valid_from, valid_until)`. The database prevents one value/scope from identifying
  multiple securities during overlapping intervals.
- **PriceBar**: one vendor's OHLCV observation for a security, interval, and price
  basis. Raw and adjusted bases are distinct records.
- **FundamentalObservation**: one published vintage of a typed issuer/security fact,
  with fiscal period, unit, revision, and filing lineage.
- **EconomicObservation**: one source series value and vintage, with reference time,
  geography, unit, and release time.
- **Filing**: metadata and immutable raw reference for one regulatory document.
- **CorporateAction**: an announced action with distinct publication and effective
  times and enough parameters to reproduce adjustments.
- **DataSource**: a provider/dataset configuration and provenance identity.
- **IngestionRun**: one idempotent execution attempt and its terminal accounting.

Canonical schemas do not preserve every vendor field. Complete source fidelity lives
in the raw artifact referenced by SHA-256. Vendor-specific typed records may exist
inside adapters but may not be imported by research packages.

## Identity and uniqueness

UUIDs are opaque and stable. A deterministic domain natural key is also specified for
idempotent writes:

- price: `(source, security, interval, observed_at, price_basis)`
- fundamental: `(source, entity, concept, period, published_at, revision, accession_number, form, frame)`
- economic: `(source, series, observed_at, published_at, revision)`
- filing: `(source, accession/document identifier)`
- corporate action: `(source, source_event_id)`
- ingestion run: `(data_source_id, run_key)`

Writers use these keys for conflict detection. Identical hashes are a no-op; a
different payload under the same key is a conflict preserved for investigation, not
an in-place update.

## Evolution

Additive optional fields may retain a major schema version. Removing a field,
changing meaning, units, key composition, or requiredness increments the major
version and requires an explicit migration. Parquet datasets store the schema version
in both rows and manifests. Readers fail closed on unsupported major versions.

## Consequences

Adapters incur a deliberate normalization step. In exchange, research is provider
replaceable, identifiers survive ticker changes, and records remain traceable to the
source evidence that produced them.
