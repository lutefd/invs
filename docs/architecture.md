# V0 architecture

The first vertical slice collects daily US equity prices, SEC company metadata and
fundamentals, and FRED macro series. It preserves source bytes, publishes canonical
Parquet, registers operational metadata in PostgreSQL, and exposes the result through
DuckDB/Jupyter and Grafana. It intentionally does not include a feature engine,
backtester, distributed queue, or live execution.

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
 atomic Parquet dataset manifest
            |
       +----+----+
       |         |
       v         v
 DuckDB/Jupyter  Grafana health
```

## Responsibilities

| Boundary | Owns | Must not own |
| --- | --- | --- |
| Collector | scheduling, request policy, raw writes, run state | research calculations |
| Source adapter | vendor request/response types | canonical storage layout |
| Validator/normalizer | semantic checks and canonical conversion | network requests |
| Raw store | immutable bytes, hashes, atomic put/get | vendor parsing |
| PostgreSQL | identity, versioned mappings, source/run metadata | bulk daily observations |
| Parquet writer | immutable analytical partitions and manifests | operational leases |
| Research | DuckDB queries, notebooks, later deterministic features | direct vendor calls |

## Data flow and commit boundary

The collector claims `(data_source_id, run_key)`, writes content-addressed raw bytes,
and emits vendor records. Validation separates rejected records from accepted records;
normalization then emits the schemas in `/schemas`. Parquet output is first written to
a temporary location and becomes visible only through an atomically installed
manifest. The run is terminal only after that manifest is durable.

There is no cross-store distributed transaction. Immutable files plus a PostgreSQL
state machine make interrupted work safe to reconcile and rerun. Dataset consumers
read committed manifests only.

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
data/normalized/<entity>/schema_version=1.0.0/year=YYYY/.../*.parquet
data/normalized/<entity>/manifests/<dataset-version>.json
```

Logical raw manifests map request/source context to content hashes. Physical keys are
implementation-neutral so an S3-compatible RawStore can replace the filesystem.

## Operational guarantees

- Collectors are safe to rerun and do not overwrite conflicting payloads.
- Empty, partial, and failed results remain distinguishable.
- Every normalized row traces to a raw SHA-256 and ingestion run.
- All instants are UTC; source-local parsing is explicit.
- New source kinds are data values, not database migrations. Run status remains a
  closed enum because it participates in checked lifecycle invariants.

Detailed rationale is recorded in [ADR 0001](adr/0001-storage-boundaries.md),
[ADR 0002](adr/0002-point-in-time-semantics.md), [ADR 0003](adr/0003-canonical-domain-models.md),
and [ADR 0004](adr/0004-failure-and-idempotency.md).
