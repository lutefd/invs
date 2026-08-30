# Canonical schemas

These JSON Schema 2020-12 documents are the provider-neutral exchange contracts for
normalized records. `common.schema.json` owns shared lossless scalar and provenance
definitions. All entity schemas reject unknown fields and pin a major contract with
`schema_version`.

`feature-set-registry.schema.json` and the checked-in
`feature-set-registry.json` describe the reviewed feature-set allowlist. Registry
entries pin their required canonical inputs, historical-fitness labels, calendar and
decision-clock policy, lookback, null behavior, computation delay, output types, and
generator implementation. The registry is planning metadata; feature rows remain in
manifest-backed Parquet and callers must resolve a feature set by exact name and
version. `feature-taxonomy-registry.schema.json` and the checked-in
`feature-taxonomy-registry.json` define the small reviewed SEC concept mapping used
by `fundamental-growth`; they are explicit, versioned, and fingerprinted into the
derived artifact input. `feature-catalog-report.schema.json` defines the read-only
coverage and lineage report for registered dataset-level feature batches; it contains
catalog metadata and hashes, never feature values. `feature-quality-report.schema.json`
defines the separate read-only feature-level diagnostic report for a batch, including
typed-null reasons, stale inputs, rejected partitions, source contribution, and raw
locators. The strict `feature-fundamental-*` and `feature-macro-*` schemas define the
manifest/observation contracts for the reviewed `fundamental-growth` and `macro-state`
families.
`feature-momentum-manifest.schema.json` and `feature-momentum-observation.schema.json`
define the strict manifest and row contracts for the registered `market-momentum`
feature set, including its six decimal-or-null outputs.

## Normalized publication boundary

Canonical normalized Parquet is published through a versioned `manifest.json` for each
dataset partition. The manifest carries the schema and normalizer versions, source and
run provenance, partition identity, total `row_count`, and a `parts` list. Each listed
part has its own row count and SHA-256, and its path must be the matching immutable
content name `part-<sha256>.parquet`:

```text
data/normalized/<dataset>/source=<source>/<entity-key>=<value>/
  manifest.json
  part-<sha256>.parquet
```

`manifest.json` is the sole canonical reader pointer. A reader validates the manifest,
checks every listed part's hash and row count, and reads only those listed parts.
`data.parquet`, unlisted Parquet files, and recursive `**/*.parquet` discovery are not
valid alternatives. Old parts may remain physically present after a new manifest is
published, but they are not committed data unless a manifest lists them.

The collector fails closed on unmanaged normalized output: pre-contract or
pre-manifest files that lack the required schema, provenance, or manifest contract,
as well as other incompatible output. The explicit recovery is to archive the
complete `data/normalized/` tree in a recoverable location, leave `data/raw/` and
PostgreSQL run/source metadata intact, recreate an empty normalized tree, and
reingest. No migration invents missing provenance.

Provider bytes are retained in the raw store before a parse/schema error is finalized
when the adapter returns them. Such a response emits no normalized schema instance or
latest-only operational snapshot; accepted rows retain `raw_payload_hash` and
`raw_record_locator` so their evidence remains addressable.

## Time and knowledge

| Field | Meaning | Determines historical availability? |
| --- | --- | --- |
| `observed_at` | When the value applies to the world | No, by itself |
| `published_at` | Earliest defensible public availability of this version | Yes |
| `effective_at` | When an action or metadata change takes economic/legal effect | No, by itself |
| `provenance.ingested_at` | When this installation durably received the raw bytes | Only for live replay |

`published_at: null` is allowed for metadata sources where public release precision
is unavailable. Such records require an explicit `available_at` supplied by the
source normalizer; readers must not substitute `observed_at`, `period_end`, or a
delivery date as a publication instant. Numeric fundamentals, macro vintages, and
corporate actions retain their stricter source-specific publication requirements.

The v1 contract includes a canonical filing-metadata dataset, and its writer,
CVM and SEC provider/collector integration, and Python research-catalog exposure
are present.
A bounded CVM IPE replay passed live acceptance at implementation commit `742e5ae`. A CVM
IPE delivery date is retained as `filing_date` and may populate
`period_end`/`observed_at` when the source supplies a reference date, but it does not
establish `published_at`. CVM IPE rows
therefore use `published_at: null` and `published_precision: "unknown"`; `available_at`
is explicit (normally durable receipt time), never derived from `period_end`, and
supports only known-to-this-installation live replay rather than historical public-
availability claims. The filing natural key is `(source, source_document_id)`, so a
source version must be part of `source_document_id` when it changes document identity.

SEC rows use the accession number as `source_document_id`, preserve the primary
document and exact EDGAR archive URL, and use the exact acceptance timestamp for
both `published_at` and `available_at`. SEC `filing_date` and optional `reportDate`
are source civil dates: `reportDate` is retained as `period_end` but neither date is
promoted to `observed_at` or used as an availability substitute. Because submissions
are a growing container, an unchanged accession remains idempotent across different
container hashes while retaining its first raw lineage; changed canonical metadata
under that accession remains a conflict.

CVM CAD is a current issuer snapshot, not versioned filing history. The collector
retains it as raw ingestion-only evidence; it is excluded from historical filing
claims and must not be joined into an as-of research snapshot.

`fx-observation.schema.json` defines the first canonical FX dataset. The admitted
source is BCB PTAX closing USD/BRL, with the pair orientation, buy and sell rates,
source fixing timezone, exact bulletin availability, revision, and local receipt
recorded explicitly. Its manifest-backed Parquet partition is
`fx/source=bcb_ptax/pair=USD-BRL/`; runtime validation additionally requires positive
rates, `buy_rate <= sell_rate`, and equality of fixing, publication, and availability
timestamps for this source contract.

The canonical price contract also admits `source=b3_cotahist` with
`price_basis=raw`. COTAHIST supplies a date observation rather than an exact close or
publication instant, so `observed_precision=date`, `published_at` is absent, and
`available_at` is the durable local receipt of the closed annual archive. Research
code must not backdate those rows before receipt.

All timestamps are UTC RFC 3339 values ending in `Z`. Financial decimals are strings
so Go, Python, JSON, and Parquet conversions do not silently round them.

### Observed-value precision

`observed_precision` is optional on `price-bar`, `fundamental-observation`, and
`economic-observation`. Its allowed values are `date`, `second`, and `unknown`.
Omission is valid for earlier valid `schema_version: 1.0.0` records and readers
interpret it as `unknown`; adding the field does not require a schema-version change.
An earlier valid v1 Parquet part may therefore omit `observed_precision` when it is
listed by a valid manifest and carries the required schema and provenance. That
compatibility case is distinct from unmanaged pre-contract or pre-manifest files,
which are rejected and handled by archive/reset. A present value outside this enum
is invalid and must fail closed rather than being coerced or treated as `unknown`.

`observed_at` may physically encode a civil date as UTC midnight only when
`observed_precision: date` is present. In that case it is a reference date, not an
exact instant. `observed_precision: second` identifies source precision at one
second, while `unknown` means the source precision is unavailable. The marker does
not change publication or availability semantics: point-in-time research continues
to use `available_at` as the conservative knowledge cutoff, and `published_at`
remains source metadata.

The initial provider mappings are explicit:

| Provider value | Source field | Canonical marker |
| --- | --- | --- |
| FRED | observation date | `date` |
| ALFRED | observation and real-time vintage dates | `date` |
| SEC | period date | `date` |
| Yahoo | bar timestamp | `second` |

Adapters must preserve these mappings and fail closed on an invalid marker. They
must not infer exact instants from a date-only value or silently reinterpret a
malformed marker.

### Historical identity and calendars

The v0.2 historical contracts are separate from the current YAML/security catalog:

- `security-identifier-version.schema.json` records a scoped identifier assignment
  with validity and knowledge intervals;
- `security-listing-version.schema.json` records issuer, MIC, currency, and primary
  listing state over a validity interval;
- `universe-membership.schema.json` records positive or corrective membership
  assertions; and
- `trading-session.schema.json` plus `calendar-manifest.schema.json` records
  explicit open/closed sessions, exchange timezone, calendar version, and a session
  fingerprint.

`available_at` is the historical knowledge cutoff, while `valid_from` and
`valid_until` describe the market or universe interval. The synthetic bounded
fixtures in `historical-truth.fixture.json` and `calendar.fixture.json` are
exercised by `python/tests/test_historical_truth.py`; they prove the resolver
boundary but do not represent live provider coverage or durable source admission.

## Validation

Run the dependency-free structural and reference check:

```bash
python3 schemas/validate_schemas.py
```

Runtime adapters must additionally validate emitted instances with a complete JSON
Schema 2020-12 implementation and domain checks that JSON Schema cannot express
cleanly, such as `low <= open/close <= high`, timestamp ordering, and half-open range
non-overlap.

## Release compatibility

`release/compatibility.json` is the v1.0 release contract. It pins the supported
Go/Python/service runtime, content-addresses the schema and migration catalog, and
fingerprints the reviewed registries, engines, and build files. The validator rejects
unknown schema additions, changed fingerprints, reordered migrations, unpinned images,
dependency drift, and unsafe host bindings:

```bash
make release-validate
```

An upgrade must run that preflight against the intended checkout, take a backup with
`make backup`, apply forward migrations only, and validate the restored installation
before it resumes collection or paper activity. A changed contract requires a new
manifest revision rather than silently accepting a mixed release.

`daily-cycle.schema.json` defines the required input references for a complete local
cycle, and `daily-cycle-report.schema.json` defines its durable stage evidence. The
cycle runner keeps the command order fixed, records one log per stage, resumes only
stages whose prior exit code was zero, and runs the final observation stage even when
a derived stage needs attention.

`research-workflow.schema.json` defines the explicit links from a dated research
report through selected backtest experiments to paper accounts. The companion
`research-workflow-report.schema.json` preserves the source hashes, acceptance
checks, data-fitness classifications, and limitations. Use `make workflow-acceptance`
to regenerate the thematic and US/Brazil cross-market integration fixtures; an
`attention` result is expected until genuine wall-clock paper evidence exists.

`paper-forward-record.schema.json` defines the genuine forward-evidence contract.
The `invs-forward-record capture` CLI binds a recent reconciled paper report to its
immutable account file, ledger manifest, and sequence bounds, preserving hashes and
UTC capture timestamps. Use `make forward-record-capture` only after a real paper
session; it cannot convert retained historical or installation-replay fixtures into
wall-clock evidence.
