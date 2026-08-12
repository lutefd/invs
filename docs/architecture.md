# V0 architecture

The first vertical slice collects daily US equity prices, SEC company metadata and
fundamentals, and FRED macro series. It preserves source bytes, publishes canonical
Parquet through immutable manifests, registers operational metadata in PostgreSQL,
and publishes accepted latest-only price/macro projections for Grafana. It exposes
canonical history through DuckDB/Jupyter. It intentionally does not include a feature
engine, backtester, distributed queue, or live execution.

```text
SEC / FRED / price provider
            |
            v
 collector + source adapter -----> PostgreSQL
            |                       security master
            |                       sources and run state
            v
 immutable raw filesystem
            |
            v
 validate + canonicalize
            |
            v
 content-named immutable Parquet part
            |
            v
 atomic manifest publication
            |
       +----+----+
       |         |
       v         v
 DuckDB/Jupyter  PostgreSQL latest-only projections -> Grafana
```

## Responsibilities

| Boundary | Owns | Must not own |
| --- | --- | --- |
| Collector | scheduling, request policy, raw writes, run state | research calculations |
| Source adapter | vendor request/response types | canonical storage layout |
| Validator/normalizer | semantic checks and canonical conversion | network requests |
| Raw store | immutable bytes, hashes, atomic put/get | vendor parsing |
| PostgreSQL | identity, versioned mappings, source/run metadata, latest-only operational projections | canonical history and bulk daily observations |
| Parquet writer | immutable content-named analytical parts and manifests | operational leases and latest-only projections |
| Research | DuckDB queries, notebooks, later deterministic features | direct vendor calls |

## Data flow and commit boundary

The collector claims `(data_source_id, run_key)` and writes content-addressed raw bytes
before parsing them. If a provider returns bytes together with a parse or schema error,
the raw object is preserved for diagnosis and replay, but that payload produces no
accepted normalized row or operational snapshot. Validation separates rejected records
from accepted records; normalization then emits the schemas in `/schemas`.

Each normalized partition is first written to a temporary Parquet file. The writer
renames it to an immutable content-named `part-<sha256>.parquet`, verifies and syncs it,
then atomically installs `manifest.json` as the commit pointer. A manifest is the only
canonical reader boundary: readers enumerate manifests for known datasets, verify the
manifest, row counts, and part hashes, and read only its listed parts. They never use
`data.parquet`, arbitrary unlisted Parquet files, or `**/*.parquet` discovery.

At source-run finalization, all candidate provenance is validated before candidates are
collapsed to one winning price per security or macro observation per series. This keeps
the PostgreSQL finalization bounded even for large historical FRED batches. One
PostgreSQL transaction then upserts the accepted latest-only projections and marks the
run terminal; a partial run may publish winners for entities that completed
successfully. The collector uses a bounded finalization context.

There is no cross-store distributed transaction. Immutable raw objects, parts, and
manifests plus a PostgreSQL state machine make interrupted work safe to reconcile and
rerun. If a process dies before finalization, its queued/running row may remain active;
automatic cancellation is not used. Only an operator may cancel a confirmed orphan
active run, with an explicit reason, and that action neither publishes nor deletes
snapshot data.

## Point-in-time query boundary

Research access is split into two explicit modes:

- `as_of(decision_at)` filters versions by public `published_at` and reconstructs the
  identifier/universe state valid at that time.
- `latest()` is a present-day convenience view and is forbidden in backtest code.

Live replay also checks `ingested_at`; historical research does not pretend the local
installation existed in the past. Adjustments and derived features carry their input
knowledge cutoff.

## Storage layout

```text
data/raw/<source>/year=YYYY/month=MM/day=DD/<sha256>.<ext>
data/normalized/<dataset>/source=<source>/<entity-key>=<value>/manifest.json
data/normalized/<dataset>/source=<source>/<entity-key>=<value>/part-<sha256>.parquet
```

The manifest carries schema/provenance metadata, partition identity, total row count,
and the content hash and row count of every immutable part. A changed partition writes
a new part and replaces the manifest pointer; an old or unlisted part is not committed
data. Logical raw manifests map request/source context to content hashes. Physical keys
are implementation-neutral so an S3-compatible RawStore can replace the filesystem.

## Operational guarantees

- Collectors are safe to rerun and do not overwrite conflicting payloads.
- Empty, partial, and failed results remain distinguishable; parse-error raw evidence is
  retained without fabricating accepted output.
- Latest-only PostgreSQL snapshots are replaceable operational projections, never
  authoritative history.
- Legacy normalized trees fail closed and require a recoverable archive/reset before
  reingestion; raw evidence is retained.
- Orphan queued/running runs require explicit operator cancellation and a reason.
- Every normalized row traces to a raw SHA-256 and ingestion run.
- All instants are UTC; source-local parsing is explicit.
- New source kinds are data values, not database migrations. Run status remains a
  closed enum because it participates in checked lifecycle invariants.

Detailed rationale is recorded in [ADR 0001](adr/0001-storage-boundaries.md),
[ADR 0002](adr/0002-point-in-time-semantics.md), [ADR 0003](adr/0003-canonical-domain-models.md),
and [ADR 0004](adr/0004-failure-and-idempotency.md).
