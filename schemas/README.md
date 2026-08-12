# Canonical schemas

These JSON Schema 2020-12 documents are the provider-neutral exchange contracts for
normalized records. `common.schema.json` owns shared lossless scalar and provenance
definitions. All entity schemas reject unknown fields and pin a major contract with
`schema_version`.

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
