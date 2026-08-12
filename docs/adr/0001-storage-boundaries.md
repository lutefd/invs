# ADR 0001: Keep operational metadata separate from analytical data

- Status: Accepted
- Date: 2026-08-12

## Context

The platform must preserve source evidence, support idempotent collectors, and make
large time-series scans convenient without introducing distributed infrastructure.
No single store is a good fit for immutable vendor payloads, transactional identity
metadata, and columnar research workloads.

## Decision

V0 uses three explicit storage layers:

1. The raw store contains immutable, byte-for-byte source artifacts. Files are
   content-addressed by SHA-256 and also referenced from a source/date partitioned
   manifest. A successful write is never replaced with different bytes.
2. PostgreSQL contains the security master, source configuration, ingestion state,
   research metadata, and other relational control-plane data. It is not the
   default home for market observations.
3. Parquet contains canonical, normalized observations and later deterministic
   features. DuckDB reads Parquet directly for notebooks, validation, and batch
   transformations.

Adapters may know vendor formats. Downstream research code only reads canonical
schemas. The normalization boundary is:

```text
vendor -> raw artifact -> validated vendor record -> canonical record -> Parquet
```

Every canonical observation links to a `data_source_id`, `ingestion_run_id`, and
`raw_payload_hash`. Dataset manifests additionally record schema version,
transformation version, Git commit, partitions, row counts, and file hashes.

Filesystem raw storage is the first implementation. Its contract must permit an
S3-compatible implementation later without changing adapters. Local paths are
implementation details and must not appear in canonical identity keys.

## Component boundaries

- Collectors schedule requests, handle rate limits/retries, persist raw bytes, and
  create/update ingestion runs.
- Source adapters decode only their vendor's response and emit typed vendor records.
- Validators reject structurally or semantically invalid records before publication.
- Normalizers convert validated records into versioned canonical models.
- Dataset writers atomically publish Parquet files and their manifest.
- PostgreSQL repositories own transactional metadata and security resolution.
- Python research and Grafana query canonical datasets and health metadata; they do
  not call vendors directly.

## Consequences

- A vendor response can be reprocessed after normalizer changes without downloading
  it again.
- Point-in-time snapshots can be reconstructed from immutable facts and provenance.
- Cross-store publication requires an explicit state machine; it is not a distributed
  transaction. An ingestion run succeeds only after raw and normalized manifests are
  durably published.
- Small metadata joins may cross PostgreSQL and DuckDB through explicit extracts.
  This is preferable to duplicating all observations in PostgreSQL.

## Rejected alternatives

- PostgreSQL for every observation: simple initially, but weakens the intended
  columnar research path and mixes control and analytical workloads.
- Parquet for mutable operational state: cannot safely provide transactional leases,
  uniqueness, and concurrent run transitions.
- Kafka, Spark, or a graph database in v0: none solves a demonstrated scale problem.
