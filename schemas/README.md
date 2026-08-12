# Canonical schemas

These JSON Schema 2020-12 documents are the provider-neutral exchange contracts for
normalized records. `common.schema.json` owns shared lossless scalar and provenance
definitions. All entity schemas reject unknown fields and pin a major contract with
`schema_version`.

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

The v1 contract includes a canonical filing-metadata dataset, and its writer is
present. CVM provider/collector integration and Python research-catalog exposure are
still staged, however, so no CVM acceptance is claimed. A CVM IPE delivery date is
retained as `filing_date` and may populate `period_end`/`observed_at` when the source
supplies a reference date, but it does not establish `published_at`. CVM IPE rows
therefore use `published_at: null` and `published_precision: "unknown"`; `available_at`
is explicit (normally durable receipt time), never derived from `period_end`, and
supports only known-to-this-installation live replay rather than historical public-
availability claims. The filing natural key is `(source, source_document_id)`, so a
source version must be part of `source_document_id` when it changes document identity.

CVM CAD is a current issuer snapshot, not versioned filing history. It is excluded
from historical filing claims and must not be joined into an as-of research snapshot.

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
| SEC | period date | `date` |
| Yahoo | bar timestamp | `second` |

Adapters must preserve these mappings and fail closed on an invalid marker. They
must not infer exact instants from a date-only value or silently reinterpret a
malformed marker.

## Validation

Run the dependency-free structural and reference check:

```bash
python3 schemas/validate_schemas.py
```

Runtime adapters must additionally validate emitted instances with a complete JSON
Schema 2020-12 implementation and domain checks that JSON Schema cannot express
cleanly, such as `low <= open/close <= high`, timestamp ordering, and half-open range
non-overlap.
