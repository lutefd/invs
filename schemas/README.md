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

The collector fails closed on legacy or incompatible normalized output. The explicit
recovery is to archive the complete `data/normalized/` tree in a recoverable location,
leave `data/raw/` and PostgreSQL run/source metadata intact, recreate an empty
normalized tree, and reingest. No migration invents missing provenance.

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

`published_at: null` is intentionally allowed only for metadata and price sources
where public release precision may be unavailable. Such records are excluded from
point-in-time research until a documented conservative availability policy resolves
the timestamp. Fundamentals, macro vintages, filings, and corporate actions require
an explicit publication instant.

All timestamps are UTC RFC 3339 values ending in `Z`. Financial decimals are strings
so Go, Python, JSON, and Parquet conversions do not silently round them.

## Validation

Run the dependency-free structural and reference check:

```bash
python3 schemas/validate_schemas.py
```

Runtime adapters must additionally validate emitted instances with a complete JSON
Schema 2020-12 implementation and domain checks that JSON Schema cannot express
cleanly, such as `low <= open/close <= high`, timestamp ordering, and half-open range
non-overlap.
