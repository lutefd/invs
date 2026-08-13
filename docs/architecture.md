# V1/v0 foundation architecture

This is the first actively developed v1/v0 foundation. The first vertical slice
collects daily US equity prices, SEC company metadata and fundamentals, current
FRED/BCB macro series, and bounded ALFRED historical vintages. It preserves source bytes, publishes canonical
Parquet through immutable manifests, registers operational metadata in PostgreSQL,
and publishes accepted latest-only price/macro projections for Grafana. It exposes
canonical history through DuckDB/Jupyter and a closed, deterministic `market-basic`
feature artifact engine. It intentionally does not include a strategy API, backtester,
distributed queue, or live execution.

The post-metadata v0 acceptance passed on 2026-08-12 at commit `9ce22d0` for SEC,
Yahoo, FRED, and BCB. Its raw run manifests and normalized evidence are retained in
the recoverable r3 archive at `/home/luis/invs-acceptance/2026-08-12-v0-r3` on the
acceptance host. That absolute path is an operator reference, not a required checkout
or portability dependency; earlier partial/failure runs remain in separate sibling
archives.

## CVM and B3 rollout boundary

CVM is integrated as a source boundary. It was not part of the original r3 vertical-
slice acceptance, but a later bounded IPE replay passed at implementation commit
`742e5ae`: 199 configured Petrobras filing rows were published with complete
provenance and identical-key retry behavior. Its configuration, PostgreSQL source-
catalog support, provider/collector path, canonical filing-metadata writer, and Python
research-catalog exposure are present. CAD remains raw ingestion-only by design.

CVM IPE metadata uses the source delivery date as `filing_date`; that date does not
establish a public publication instant. The integrated collector stores the source
resources raw, maps IPE rows only through exact configured CVM codes, and publishes
canonical filings under `source=cvm_ipe`. IPE rows therefore retain
`published_at = null` with `published_precision = unknown`, and receive an explicit
conservative `available_at` equal to durable receipt time. This supports a
"known-to-this-installation" live replay after collection, not a claim about historical
public availability. A filing's reference date may populate
`period_end`/`observed_at`, but never determines `available_at`.

CVM CAD is a current issuer snapshot, not versioned filing history. It is retained as
raw ingestion-only evidence, excluded from historical filing claims, and must not be
joined into an as-of research snapshot. The Python catalog exposes dedicated
`filings_canonical`, `filings`, and `filings_as_of(...)` interfaces. The notebook still
needs a separate optional filings-inspection cell; filings will not be joined
one-to-many into the existing price/fundamental/macro snapshot.

B3 market data is not implemented in this foundation. The source-selection discovery
for future Brazilian coverage is Yahoo Finance as the primary bridge, with Brazilian
tickers mapped through the `.SA` suffix for historical prices, volumes, dividends,
splits, and related market data. Selective B3 public datasets may enrich instrument
metadata, delistings, corporate actions, and validation without making paid B3
credentials a dependency of the main analytical pipeline.

This is a planning boundary, not a historical-availability claim. Unattended access,
source terms, captured fixtures, instrument mapping, coverage, rate limits, and the
required market-data policy must still be established before Brazilian support is
accepted.

```text
SEC / FRED / ALFRED / BCB / Yahoo / CVM provider
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
| Research | DuckDB queries, notebooks, deterministic feature artifacts | direct vendor calls, strategy execution |

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

## Database migrations

Fresh PostgreSQL volumes apply the forward migrations in order: `000001_core_metadata`,
`000002_latest_observation_snapshots`, `000003_observed_precision`, and
`000004_run_inputs`, and `000005_nullable_macro_snapshot_value`. Existing initialized volumes use `make migrate`, which conditionally
applies missing changes in order; its schema checks make rerunning the
command idempotent. `000001` is the base schema created during volume initialization.

## Point-in-time query boundary

Research access is split into an explicit point-in-time mode and a present-day
convenience mode:

- `research_snapshot(decision_at, macro_source=...)` requires both `available_at <= decision_at` and
  `observed_at <= decision_at`. `available_at` is the explicit conservative knowledge
  cutoff used by research; it may incorporate source publication precision and any
  documented source-specific delay. `published_at` remains source metadata and is not
  silently substituted as the query cutoff.
- The current YAML `universe` entries provide the configured `security_id` to
  `issuer_id` links used by this research slice. They are current configuration
  mappings, not historical identifier resolution, and are not reconstructed by
  `decision_at`.
- `latest()` is a present-day convenience view and is forbidden in backtest code. The
  FRED and BCB projections are current-vintage latest rows. ALFRED's authoritative
  history remains in manifest-backed Parquet while PostgreSQL exposes only its latest
  operational projection. Yahoo backfills likewise provide only the provider data
  returned and collected at ingestion time.

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
- Grafana exposes latest-only price and macro projections; SEC remains ingestion-only
  because there is no fundamental snapshot table. CVM CAD is raw ingestion-only, while
  CVM IPE is available through the canonical filing dataset and Python filing catalog.
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
