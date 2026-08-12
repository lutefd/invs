# V1/v0 foundation architecture

This is the first actively developed v1/v0 foundation. The first vertical slice
collects daily US equity prices, SEC company metadata and fundamentals, and FRED
macro series. It preserves source bytes, publishes canonical
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

Research access is split into an explicit point-in-time mode and a present-day
convenience mode:

- `research_snapshot(decision_at)` requires both `available_at <= decision_at` and
  `observed_at <= decision_at`. `available_at` is the explicit conservative knowledge
  cutoff used by research; it may incorporate source publication precision and any
  documented source-specific delay. `published_at` remains source metadata and is not
  silently substituted as the query cutoff.
- The current YAML `universe` entries provide the configured `security_id` to
  `issuer_id` links used by this research slice. They are current configuration
  mappings, not historical identifier resolution, and are not reconstructed by
  `decision_at`.
- `latest()` is a present-day convenience view and is forbidden in backtest code.

Macro latest-row selection uses the same total order as PostgreSQL finalization:
`observed_at DESC`, `revision DESC`, `available_at DESC`, `ingested_at DESC`, then
`raw_payload_hash DESC`. Live replay may additionally check `ingested_at`; historical
research does not pretend the local installation existed in the past. Adjustments and
derived features carry their input knowledge cutoff.

## Storage layout

```text
data/raw/<source>/year=YYYY/month=MM/day=DD/<sha256>.<ext>
data/raw/runs/<source>/<ingestion-run-id>/manifest.json
data/normalized/<dataset>/source=<source>/<entity-key>=<value>/manifest.json
data/normalized/<dataset>/source=<source>/<entity-key>=<value>/part-<sha256>.parquet
```

The normalized manifest carries schema/provenance metadata, partition identity, total
row count, and the content hash and row count of every immutable part. A changed
partition writes a new part and replaces the manifest pointer; an old or unlisted part
is not committed data. Each ingestion run also publishes a strict version-1 logical raw
manifest at the RawStore key
`runs/<source>/<ingestion-run-id>/manifest.json`. It contains `version`, `source`,
`ingestion_run_id`, and sorted `entries`. Each entry contains `logical_key`,
`object_key` (a RawStore key, never a local path), the actual stored `sha256`, `size`,
`content_type`, `fetched_at`, and sorted `attributes`. The manifest hash is the
lowercase SHA-256 of the exact UTF-8 indented JSON bytes including its trailing newline;
that value is stored in `ingestion_runs.raw_payload_manifest_hash`. Empty runs publish
the same contract with an empty `entries` array. Replay loads this key and verifies each
listed object's bytes and size through `RawStore.Get` before use. Physical keys are
implementation-neutral so an S3-compatible RawStore can replace the filesystem.

## Operational guarantees

- Collectors are safe to rerun and do not overwrite conflicting payloads.
- Empty, partial, and failed results remain distinguishable; parse-error raw evidence is
  retained without fabricating accepted output.
- Latest-only PostgreSQL snapshots are replaceable operational projections, never
  authoritative history.
- Unmanaged pre-contract or pre-manifest normalized data that lacks the required
  schema, provenance, or manifest contract fails closed and requires a recoverable
  archive/reset before reingestion; raw evidence is retained. Earlier valid v1
  Parquet parts may omit optional `observed_precision` when listed by a valid
  manifest, which is distinct from unmanaged pre-contract data.
- Orphan queued/running runs require explicit operator cancellation and a reason.
- Every normalized row traces to a raw SHA-256 and ingestion run.
- All instants are UTC; source-local parsing is explicit.
- New source kinds are data values, not database migrations. Run status remains a
  closed enum because it participates in checked lifecycle invariants.

Detailed rationale is recorded in [ADR 0001](adr/0001-storage-boundaries.md),
[ADR 0002](adr/0002-point-in-time-semantics.md), [ADR 0003](adr/0003-canonical-domain-models.md),
and [ADR 0004](adr/0004-failure-and-idempotency.md).
