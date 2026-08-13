# Personal Quant Research Platform — Current State Handoff

This document records the repository as it exists at the handoff boundary. It is
intended for the next engineering session, not as a product brochure. The
repository is the first actively developed version of the platform. It is not a
deprecated production version.

## Post-handoff continuation update

On 2026-08-13, the first three immediate actions from this handoff were completed:

- README, architecture, schema guidance, and ADR wording were aligned with the
  accepted CVM IPE boundary and implemented `market-basic` engine.
- The lingering zero-record FRED run was confirmed orphaned and explicitly
  cancelled; PostgreSQL subsequently reported zero queued/running runs.
- A supported `make feature` / `make feature-validate` operator path was added and
  exercised against the real manifest-backed AAPL slice, including identical replay
  and the exact point-in-time cutoff.

The durable evidence is in
[the market-basic operator acceptance report](acceptance/2026-08-12-market-basic.md).

The next data-integrity milestone has now also been implemented:

- ALFRED is a separate disabled-by-default macro source using the official FRED
  observations endpoint with historical real-time periods and explicit replay bounds.
- Raw JSON pages are persisted before canonical publication, with per-page hashes,
  non-secret run inputs, deterministic per-observation revisions, explicit null
  vintages, and a conservative 36-hour availability policy for date-only vintages.
- Historical ALFRED writes are idempotent and preserve A -> B -> A sequences without
  revision drift. Current FRED and ALFRED series remain source-separated in research.
- PostgreSQL's latest macro projection now permits an explicit null value; authoritative
  historical vintages remain in manifest-backed Parquet.
- Provider, collector, normalizer, metadata, research-cutoff, and dashboard tests
  pass. A bounded credentialed CPIAUCSL live run, exact-key skip, and independent
  zero-row canonical replay also passed; see the
  [ALFRED acceptance report](acceptance/2026-08-13-alfred-cpiaucsl.md).

The first v0.1 data-integrity unit then landed in `8f2680f`: every provider now
returns a common downloaded-resource collection alongside parse errors, and the
collector persists that collection before finalizing a failed resource. Contract
tests cover malformed Yahoo, FRED, BCB, SEC, ALFRED, and CVM responses, including
partial multi-resource downloads.

The platform now has a canonical [full-version roadmap](full-version-roadmap.md) and
a granular [roadmap execution index](roadmap/README.md), introduced at `cec0ed4`.
The roadmap, rather than the old numbered actions at the bottom of this historical
handoff, is the sequencing authority for future versions. The notebook now has
separate empty-safe CVM filing and feature-artifact inspection sections from
`806874a`; no strategy, backtest, portfolio, or ML boundary was introduced.

The recovery slice then landed in `0f73e39`: a read-only reconciliation report,
explicit destination backup/restore scripts, and a clean-root PostgreSQL/data
restore drill. The daily schedule and the final v0.1 acceptance gate remain open;
see [the recovery runbook](operations-recovery.md).

## B3 market-data source-selection discovery

The current planning discovery for Brazil is to use Yahoo Finance as the primary B3
market-data bridge. Brazilian tickers should be mapped through the `.SA` suffix to
collect historical prices, volumes, dividends, splits, and related market data. This
keeps paid B3 credentials out of the critical path for the platform's medium- to
long-term portfolio research, backtesting, simulation, and ML use cases.

B3 public datasets remain selective complements for instrument metadata, delistings,
corporate actions, and validation. This changes the source-selection plan, not the
implementation status: no Brazilian market-data integration, historical-fitness
acceptance, or broad instrument discovery is present in this checkout yet. The v0.2
work must still validate access and terms, fixtures, identifiers, coverage, rate
limits, and explicit availability semantics before accepting the bridge.

## Current continuation boundary

- Repository: `/home/luis/dev/invs`
- Branch: `main`
- Live-accepted ALFRED implementation boundary: `31378be` (`docs: record ALFRED milestone and roadmap`)
- Latest provider-contract implementation boundary: `8f2680f` (`feat(provider): standardize downloaded resource results`)
- Latest research-visibility implementation boundary: `806874a` (`feat(research): inspect filings and feature artifacts`)
- Latest operations implementation boundary: `0f73e39` (`feat(operations): reconcile durable ingestion state`)
- Latest roadmap discovery boundary: `0be506c` (`docs(roadmap): record Yahoo B3 market-data bridge discovery`)
- ALFRED credentials remain environment-only; do not put them in YAML, run metadata,
  raw attributes, logs, or acceptance artifacts.
- The older `742e5ae` implementation point below remains useful as the exact original
  handoff baseline, but it is no longer the current repository boundary.

## Historical implementation handoff point

- Repository: `/home/luis/dev/invs`
- Branch: `main`
- Implementation HEAD: `742e5ae48bd0ad05c496536b6aecf4d5e9dfd241`
- Implementation HEAD subject: `feat(features): publish deterministic market artifacts`
- The two documentation commits that add this handoff and the usage guide follow
  that implementation boundary; the code state they describe is the exact commit
  above.
- Handoff date: 2026-08-12 (local runtime observations may cross UTC midnight)
- At the implementation handoff, no tracked or unrelated implementation changes
  were present; this handoff and the separately requested usage guide were the only
  pending documentation outputs.
- PostgreSQL, Jupyter, and Grafana containers: running and healthy at the time of inspection
- This document is intentionally a separate documentation slice. It must be reviewed and committed by the orchestrator; the document-writing worker must not commit it.

The original handoff described two acceptance boundaries:

1. The original post-metadata v0 acceptance for SEC, Yahoo, FRED, and BCB passed
   at `9ce22d0`. Its recoverable evidence is retained at
   `/home/luis/invs-acceptance/2026-08-12-v0-r3`.
2. CVM integration and the deterministic feature engine advanced beyond that
   acceptance. The fresh post-fallback CVM acceptance against implementation HEAD
   `742e5ae` passed; its external evidence archive and exact checks are recorded in
   [Current CVM live evidence status](#current-cvm-live-evidence-status) below. The
   acceptance report is not a repository file, but the replay itself is complete and
   reviewable from the retained archive.

The local bind-mounted checkout currently contains valid manifest-backed Yahoo and
FRED/ALFRED output plus raw evidence and two inspected `market-basic` artifacts.
The CVM live-run evidence was written to separate acceptance archives rather than
to this checkout. The feature artifacts remain replaceable derived outputs; the
recovery backup includes them and validates their selected normalized lineage.

## Mission and architectural boundaries

The platform is a small self-hosted personal quant-research foundation. Its job is
to collect vendor data with explicit knowledge timestamps, preserve the original
bytes, publish validated canonical analytical data, and make point-in-time research
reproducible. It is not yet a trading system.

The durable truth flow is:

```text
provider response
      |
      v
immutable raw bytes + hash + run manifest
      |
      v
validated canonical model
      |
      v
content-named Parquet part + atomic manifest
      |
      +--> DuckDB / Python research / deterministic features
      |
      +--> PostgreSQL latest-only operational projection --> Grafana
```

The boundaries are deliberate:

| Boundary | Owns | Must not become |
| --- | --- | --- |
| Collector | source scheduling, request policy, raw-first ordering, run lifecycle, orchestration | a research or feature-calculation engine |
| Provider adapter | vendor URLs, response parsing, source-specific timestamps and locators | the canonical storage layout |
| Raw store | immutable bytes, metadata, hashes, recovery reads | a vendor parser or a mutable cache |
| Validator/normalizer | semantic validation and vendor-neutral records | network access or operational leases |
| Canonical Parquet | authoritative historical analytical records and manifests | a latest-only dashboard cache |
| PostgreSQL | identities, versioned identifiers, source catalog, run state, run inputs, replaceable projections | bulk canonical history |
| DuckDB/Python | manifest-backed queries, explicit point-in-time joins, deterministic derived artifacts | direct vendor calls |
| Grafana | operational health, current projections, visible coverage gaps | synthetic observations or historical research |

Raw evidence and canonical Parquet are authoritative. PostgreSQL projections may
be rebuilt or replaced. A dashboard value must never be treated as the historical
dataset merely because it is convenient to query.

Primary design references are [README.md](../README.md),
[architecture.md](architecture.md), and [ADR 0001](adr/0001-storage-boundaries.md)
through [ADR 0005](adr/0005-deterministic-feature-artifacts.md).

## Completed commit sequence

The branch is linear. These are the commits present before this handoff document
and the separate usage guide are committed:

```text
851fbd4 chore: initialize local development workspace
e9ff43c docs: define point-in-time data architecture
b48c114 feat: add idempotent market data collectors
561d333 feat: add local research and observability runtime
3299471 feat: add reproducible research operations
2bd1b97 feat: persist trustworthy ingestion metadata
e2168b8 feat(storage): add latest observation projections
2dd7cfb feat(data): enforce canonical parquet v1
a2cc94b feat(research): support canonical parquet v1
0e72bad feat(observability): add latest snapshot dashboard
b0f10c1 feat(collector): stamp canonical provenance
fe6d6c5 feat(collector): publish monotonic operational snapshots
8b08f84 fix(metadata): bound snapshot finalization batches
cb943cc fix(provider): preserve SEC partial raw evidence
764b490 test(observability): smoke all Grafana dashboards
cfa31b1 docs: align publication and recovery boundaries
60b4e61 feat(storage): publish canonical data through manifests
abfa909 fix(data): align macro snapshot revisions
30c4afe fix(runtime): propagate collector Git provenance
e2160a8 feat(storage): persist raw run manifests
7cd98b1 fix(data): make identical prices idempotent
723f612 feat(operations): add orphan run cancellation
15521ce fix(data): reject sub-microsecond timestamps
d5b9ed2 fix(research): align point-in-time availability semantics
cc2f22b fix(provider): mark FRED release precision unknown
825c4c3 docs(schema): define observed time precision
e5a640b feat(research): support observed time precision
19010f3 feat(data): preserve observed time precision
4bcaef4 feat(storage): add observed precision snapshots
3abc3d2 feat(provider): add BCB SGS adapter
a590eb5 docs: clarify pre-contract data terminology
4680cdb feat(collector): integrate BCB SGS ingestion
983560b feat(observability): expose BCB snapshots
1326aba fix(collector): align receipt timestamps
659b987 fix(data): preserve SEC filing identities
9ce22d0 feat(metadata): persist effective run inputs
573ac75 docs: record v0 acceptance
1da8541 feat(config): add CVM source catalog
4ede8ce feat(data): add canonical filing metadata
153c568 feat(provider): add CVM source adapter
715c084 docs: define staged CVM boundary
d298cb0 feat(research): expose filing catalog
4282431 feat(collector): integrate CVM filings
092361e docs: record CVM integration boundary
5e0a7d6 fix(provider): tolerate CVM quote defects
8c2015e feat(research): expose point-in-time inputs
38adddc feat(schema): define deterministic feature artifacts
9b8dad0 fix(provider): retain CVM filings with URL identity
e81684e fix(collector): ignore unconfigured CVM issuers
742e5ae feat(features): publish deterministic market artifacts
```

The last three implementation commits are the post-contract CVM/feature additions:
`9b8dad0` adds the blank-protocol URL identity fallback, `e81684e` stops the global
CVM archive from turning unconfigured issuers into rejects, and `742e5ae` adds the
bounded `market-basic` engine. The two documentation files are intentionally not
included in this list until the orchestrator reviews and commits them.

## What is completed

### Identity, configuration, and run lifecycle

The committed configuration and metadata path provides:

- Stable UUID-based issuers, securities, source rows, and ingestion runs.
- Versioned security identifiers with validity ranges and exclusion constraints.
- Exact configured security-to-issuer mappings for the current research universe.
- Open-vocabulary source kinds, so adding a provider does not require a database enum migration.
- Effective run-input metadata, including a canonical JSON SHA-256, provider settings,
  configured entities, source vintage, and requested resources.
- A logical `(data_source_id, run_key)` idempotency key.
- Successful-run retry skipping.
- Rejection of active-key reuse and reuse of keys that ended `partial`, `failed`, or
  `cancelled`.
- Terminal statuses `succeeded`, `partial`, `failed`, and `cancelled`, with received,
  written, rejected, raw-object, raw-byte, cursor, error, and duration metadata.
- Explicit operator cancellation for a confirmed orphan queued/running run. There is
  no automatic timeout cancellation.

Canonical collection requires PostgreSQL. `SyncCatalog` creates or updates source,
issuer, security, and identifier records first. `StartRun` returns both the stable
run UUID and its `data_source_id`; production code does not invent lineage IDs that
are absent from the catalog. Isolated tests may inject UUID fixtures.

### Raw evidence and recovery

The file raw store in [internal/storage/raw.go](../internal/storage/raw.go) is
immutable by logical key:

- The stored bytes are hashed with SHA-256 and metadata records source, content type,
  fetch time, size, and attributes.
- Reusing a key with different bytes is an immutable conflict.
- Every run publishes a version-1 raw manifest under
  `runs/<source>/<ingestion-run-id>/manifest.json`.
- Raw manifest entries contain logical key, RawStore object key, actual hash, size,
  content type, fetch time, and sorted attributes. They never depend on a local
  filesystem path.
- Manifest bytes are canonical JSON with a trailing newline; the exact bytes are
  hashed and the hash is stored on `ingestion_runs`.
- `LoadAndVerifyRawManifest` reads every listed object back through the RawStore and
  checks both bytes and metadata.

Collector orchestration stores raw bytes before parsing/normalizing. After storage it
checks the adapter-reported hash against the durable stored hash. Only then does it
stamp accepted rows with source UUID, run UUID, raw hash, raw record locator, and the
aligned ingestion timestamp.

The provider recovery contract is now uniform: each adapter returns every response
body downloaded before a transport or parse/schema error in its common resource
collection. CVM and SEC retain their source-specific metadata through that same
collection, while legacy compatibility fields remain for local callers.

### Reconciliation and restore operations

The current operations boundary is [docs/operations-recovery.md](operations-recovery.md).
`make reconcile` is read-only and verifies active runs, raw manifests/objects,
normalized manifest-listed parts, unlisted Parquet, and feature input lineage.
`make backup` and `make restore` use explicit new destinations and hash-check each
immutable file. The clean-root drill at `0f73e39` restored PostgreSQL to a new
`restore_*` database and returned zero reconciliation findings; it did not claim
the remaining full v0.1 acceptance gate.

### Canonical Parquet and manifests

The Go model and writer in [internal/model/model.go](../internal/model/model.go),
[internal/normalize/parquet.go](../internal/normalize/parquet.go), and
[internal/normalize/manifest.go](../internal/normalize/manifest.go) enforce
canonical schema `1.0.0`.

Important invariants:

- Exact decimal lexemes are stored as canonical UTF-8 strings. Scientific notation,
  malformed decimal forms, and ingestion-time rounding are rejected.
- Prices are non-negative and obey OHLC ordering. Volumes are non-negative.
- Temporal order is validated: observed, published, available, ingested, with source
  precision explicitly represented as `date`, `second`, or `unknown`.
- Fundamental period ordering is validated.
- Published rows require source UUID, run UUID, raw SHA-256, record locator,
  ingestion time, and normalizer version.
- Top-level and provenance raw hashes must agree.
- A changed raw hash for the same natural key is a conflict.
- Fundamental natural keys include taxonomy and unit.
- Macro revisions preserve an A -> B -> A history rather than collapsing by value.
- Existing files are checked for physical schema, supported version, row invariants,
  duplicate natural keys, partition identity, manifest row counts, content hashes,
  and part hashes.
- Parts are immutable and content-named as `part-<sha256>.parquet`.
- `manifest.json` is the only committed reader pointer. Readers do not glob
  `data.parquet`, unlisted parts, or arbitrary recursive Parquet files.
- Publication writes and fsyncs a part, then atomically publishes the manifest.
- A pre-v1 or pre-manifest file fails closed with an actionable migration/reset error;
  it is not overwritten or silently upgraded.

Earlier valid v1 parts may omit the later optional `observed_precision` physical
column. Readers interpret that omission as `unknown` only when the part is already
listed by a valid manifest. This is not permission to accept unmanaged files.

The terms **pre-contract** and **pre-manifest** mean an unmanaged artifact from this
same in-progress first version that was created before the current storage contract
was completed. They do **not** mean a deprecated production version. If such a file
is encountered, the safe action is to archive the complete normalized tree in a
recoverable location, recreate an empty normalized root, preserve raw evidence and
the PostgreSQL catalog, and reingest. Missing provenance must not be invented by an
audited-looking in-place rewrite unless a separate evidence-backed decision proves it
can be reconstructed.

### PostgreSQL operational metadata and projections

The migrations in [migrations](../migrations) provide:

- Core security master, identifiers, data sources, and ingestion runs.
- `market_price_snapshots`, a latest-only price projection.
- `macro_observation_snapshots`, a latest-only current-vintage macro projection.
- Observed-time precision columns.
- Run-input metadata validation.

Projection rows carry source/run foreign keys, exact decimal text, raw hashes,
timestamps, precision, UUID, revision, vintage, and OHLC constraints. Finalization
collapses candidates in memory using the same total order as SQL, then writes accepted
price/macro projections and the terminal run metadata in one PostgreSQL transaction.
The ordering is:

- Price: `observed_at`, then `available_at`, then `ingested_at`, then raw hash.
- Macro: `observed_at`, then `revision`, then `available_at`, then `ingested_at`,
  then raw hash.

Older candidates cannot replace newer projections. A partial multi-entity run can
publish successful entities while an entity with a parse or storage error publishes
no snapshot. Filings do not enter these snapshot tables.

### Research and point-in-time semantics

[python/research/catalog.py](../python/research/catalog.py) registers:

- `prices_canonical`
- `fundamentals_canonical`
- `macroeconomics_canonical`
- `filings_canonical`

The lossless canonical views preserve exact value strings, flags, timestamps, and
provenance. The shorter research views add `DECIMAL(38,18)` and explicitly lossy
`DOUBLE` analysis projections; exact `*_value` or `value_text` columns remain
available.

The catalog fails closed for missing/legacy schema versions, unsupported versions,
missing fields, invalid manifests, hash/row-count mismatches, numeric physical
columns where canonical strings are required, malformed decimal strings, invalid
partition identity, duplicate JSON keys, and unlisted parts.

`research_snapshot(decision_at=..., macro_source=...)` performs explicit as-of selection. It requires
both `available_at <= decision_at` and `observed_at <= decision_at`, applies the
configured security-to-issuer mapping, and prevents accidental cross-issuer joins.
The YAML universe mapping is current configuration, not a historically versioned
identifier-resolution system.

`ResearchCatalog.point_in_time_inputs(...)` is the input boundary for derived
features. It selects manifest-backed price rows for one security at an explicit
decision timestamp and retains close, high, low, volume, availability, raw hash,
manifest path/hash, and part lineage. It does not use latest-only PostgreSQL
snapshots or silently forward-fill.

`filings_as_of(...)` is intentionally separate. Its `historical` mode is only
defensible for datasets with historical public-availability semantics; CVM IPE's
`installation_replay`/`known_at_installation` mode means the installation knew the
row after it collected the source resource. It is not a historical public filing
availability claim.

### Observability

Grafana dashboards are provisioned from
[docker/grafana/dashboards](../docker/grafana/dashboards):

- Pipeline health shows run status, failures, partials, rejected counts, raw bytes,
  and recent activity.
- The market dashboard lists configured securities even when no snapshot exists,
  shows explicit no-snapshot states, and displays only accepted latest Yahoo/FRED/ALFRED/BCB
  projections.
- SEC is labeled ingestion-only because no fundamental snapshot table exists.
- CVM filings are not presented as market snapshots.
- Missing observations are never replaced with zeros or synthetic values.

[python/research/dashboard_smoke.py](../python/research/dashboard_smoke.py) rejects
duplicate JSON keys and emits PostgreSQL `EXPLAIN` statements for all dashboard SQL.

## Provider coverage

### Yahoo daily prices

- Source code: [internal/providers/yahoo/client.go](../internal/providers/yahoo/client.go)
- Config/command: `providers.prices`, `make ingest SOURCE=prices`
- Supports URL escaping for symbols such as `^BVSP` and `BRK/B`.
- Produces canonical daily OHLCV rows with exact numeric lexemes and provenance.
- Current Yahoo downloads are conservative installation-knowledge data. A row's
  trading date is not treated as proof that the row was knowable on that date.
- The v0 acceptance retained 1,661 normalized price rows for the configured slice.

### SEC company metadata and facts

- Source code: [internal/providers/sec/client.go](../internal/providers/sec/client.go)
- Config/command: `providers.sec`, `make ingest SOURCE=sec`
- Handles quoted or numeric CIK/SIC forms, company metadata, submissions, and
  company facts.
- Preserves original numeric lexemes and rejects malformed/non-finite values.
- Uses exact filing acceptance timestamps when SEC supplies them; otherwise applies
  the adapter's conservative fallback rather than treating a filed date as an exact
  instant.
- SEC facts become canonical fundamental observations. The provider also parses
  filing metadata needed for acceptance-time reasoning, but the current collector
  does not publish SEC filing metadata into the canonical filing dataset.
- The v0 acceptance retained 25,135 normalized SEC fact rows from 26,136 received
  records and two raw objects.

### FRED

- Source code: [internal/providers/fred/client.go](../internal/providers/fred/client.go)
- Config/command: `providers.fred`, `make ingest SOURCE=fred`
- Current-vintage series are stored as canonical macro observations with revision and
  vintage fields where available.
- Non-finite values are rejected.
- FRED release precision is treated conservatively as unknown when an exact release
  instant is not supplied.
- A current-vintage pull is not a historical vintage store and cannot prove what was
  knowable on an earlier date.
- The v0 acceptance retained `DGS10` and `CPIAUCSL` output (16,137 and 954 rows in
  the retained r3 acceptance report).

### ALFRED historical vintages

- Source code: [internal/providers/alfred/client.go](../internal/providers/alfred/client.go)
- Config/command: `providers.alfred`, `make ingest SOURCE=alfred`
- The provider is disabled by default and requires environment-only `FRED_API_KEY`.
- Requests use `output_type=1`, the complete supported real-time left boundary,
  an explicit closed right boundary, and paginated JSON raw objects.
- Date-only vintage starts become `published_at`/`vintage_at` with date precision;
  the safe research availability cutoff is 36 hours later.
- Canonical revisions are deterministic ordinals per observation date, including
  equal-value and explicit missing vintages. Historical reruns do not auto-renumber.
- Fixture/unit/integration acceptance and the bounded live CPIAUCSL replay pass.

### BCB SGS

- Source code: [internal/providers/bcb/client.go](../internal/providers/bcb/client.go)
- Config/command: `providers.bcb`, `make ingest SOURCE=bcb`
- Series are configured with code, geography, unit, frequency, seasonal adjustment,
  and optional date bounds.
- Output is canonical macro data with exact strings, source metadata, and provenance.
- Like current FRED data, BCB backfills are current-vintage installation evidence,
  not an historical vintage reconstruction.
- The v0 acceptance retained 2,416 normalized rows.

### CVM IPE and CAD

CVM is integrated behind an explicit staged boundary. Configuration is in
[config/config.go](../config/config.go) and
[config/config.example.yaml](../config/config.example.yaml). The default example
keeps CVM disabled. CVM IPE archives are selected by explicit years; document URLs
are not discovered by crawling.

#### IPE

- The provider stores the IPE metadata response, each requested yearly ZIP, parser
  metadata, and hashes as raw resources before canonical publication.
- Only an exact configured `universe[].cvm_code` mapping selects a row for canonical
  publication. The archive is global, so rows for issuers outside the configured
  universe are expected and are ignored, not counted as malformed rejects. Cursor
  fields expose `ipe_rows_matched`, `ipe_rows_unconfigured`, `ipe_rows_ignored`, and
  `ipe_rows_ambiguous`.
- Duplicate configured CVM codes are an error. A source row cannot be guessed into
  an issuer by name or legal-entity text.
- Canonical rows use `source=cvm_ipe` and the filing schema. The source delivery date
  is retained as `filing_date`; it is not silently promoted to a public publication
  instant.
- `published_at` is null with `published_precision=unknown` when the source does not
  provide a defensible publication instant. `available_at` is durable receipt time,
  so the result supports installation replay only. A reference/period date may fill
  `period_end` or `observed_at`, but never `available_at`.

#### Blank protocol identity fallback

The official IPE archive contains valid rows whose `Protocolo_Entrega` field is
blank. Those rows are retained when and only when the download URL contains numeric
`numProtocolo`, `numSequencia`, and `numVersao` parameters and the URL version agrees
with the source version field.

For these rows:

- The original blank `Protocol`/`AccessionNumber` is preserved as blank.
- The exact source URL and all source fields remain in the canonical/raw evidence.
- The deterministic `SourceDocumentID` is
  `cvm-ipe:<cvm_code>:urlsha256-<sha256-of-the-exact-URL>:v<version>`.
- Missing or invalid URL identity parameters remain row-level rejects.

This is a defensible identity fallback because it derives identity from the
authoritative document locator without pretending the missing source field existed.
It is not a fabricated protocol number.

The adapter tolerates literal bare quotes found in the official semicolon-delimited
IPE/CAD extracts with the parser's lazy-quote mode, preserves the literal text, and
continues with row-level errors for invalid records. It still enforces the expected
field count and required identity/date/URL fields.

#### CAD

CVM CAD is a current issuer snapshot, not versioned filing history. It is retained as
raw evidence only. There is no canonical CAD filing dataset or latest CAD projection,
and CAD must not be joined into a historical/as-of filing or market snapshot.

When CAD is enabled, the collector records `cad_status=ingestion_only_not_published`
and `cad_rows_not_published`. Those rows are counted in the run's non-published
rejection metric so the run is normally `partial`; this is an explicit boundary, not
an accidental parser failure. A future CAD feature must first define a versioned
issuer-metadata contract rather than quietly treating a current extract as history.

#### Current CVM live evidence status

After the URL-identity and unconfigured-row fixes, the fresh official IPE replay
passed against implementation HEAD `742e5ae`.

Evidence archive:
`/home/luis/invs-acceptance/2026-08-12-cvm-ipe-current-Q8LklS`

- Run `9953e213-bbec-48a4-9151-f58ed9951baf` finished `succeeded`.
- The logical run received 30,232 rows, selected 199 Petrobras-code `9512` rows,
  wrote 199 canonical filings, and rejected 0 rows.
- 29,934 global-archive rows were explicitly ignored as unconfigured; there were
  0 ambiguous mappings.
- Two raw resources were retained, totaling 1,424,682 bytes.
- Eight blank-protocol rows were retained through validated URL identity, and 314
  literal-quote rows were accepted.
- ZIP SHA-256:
  `6b706bc15afc6d420189d38f3d54ae7c079d759811eace27f13b0d1eb8576e12`.
- Raw manifest SHA-256:
  `f05840c862360fcfe110d2d2b313b744dd5cf4cba6398897aed99da4957787b5`.
- Canonical manifest SHA-256:
  `5a09d617d186743716006c7b22bc207076f8576126eb3810ec80ca183affa543`.
- Canonical part SHA-256:
  `330bc135d8790e773b5d7b91478c42204b58b3f44a2c2ac197ad15271eb415e7`.
- DuckDB/Python readback found 199 filing rows, no missing provenance, and all
  eight blank-protocol rows using deterministic URL identities. The exact
  `available_at` boundary was `2026-08-13T00:07:51.508405Z`: one microsecond before
  returned 0 rows, at the boundary returned 199, and one microsecond after returned
  199.
- Retrying the same successful logical key exited successfully and skipped without
  rewriting raw or normalized files.

The CAD canary also passed its parser/raw-preservation check at
`/home/luis/invs-acceptance/2026-08-12-cvm-cad-current-v3rGsD`. It received and
parsed 2,677 rows, retained 1,493,128 raw bytes with SHA-256
`1035da156d0ffe2da8e809ad098387f0d7a88941eee3da77043782f6a4c5a6e7`, and accepted
7 literal-quote rows with 0 shape errors. Its `partial` terminal state is expected:
CAD remains explicitly raw-ingestion-only, so its 2,677 rows were not published as
canonical records. The official CVM acceptance gate is complete; CAD canonical
publication remains intentionally out of scope.

## Deterministic feature artifacts

The feature contract was introduced in [ADR 0005](adr/0005-deterministic-feature-artifacts.md)
and the schemas [feature-observation.schema.json](../schemas/feature-observation.schema.json)
and [feature-manifest.schema.json](../schemas/feature-manifest.schema.json). The
bounded implementation is in [python/research/features.py](../python/research/features.py).

The closed `market-basic` 1.0.0 registry contains exactly:

- `close`: selected daily close;
- `return_1d`: `close_t / close_(t-1) - 1`;
- `range_1d`: high minus low; and
- `volume`: selected volume, or null when unavailable.

The engine exposes `compute_market_basic_features`, `publish_market_basic`,
`build_market_basic_artifact`, `read_feature_artifact`,
`validate_feature_artifact`, and `compute_input_fingerprint`.

The current engine:

- selects inputs through `ResearchCatalog.point_in_time_inputs`;
- uses `Decimal` calculations and emits exact decimal strings or null, never JSON
  floating-point feature values;
- does not forward-fill missing prerequisites;
- records `decision_at`, maximum selected input availability, computation delay, and
  derived feature `available_at`;
- fingerprints the canonical input-selection envelope, including selected manifest
  and part hashes;
- writes an immutable content-named Parquet part and a manifest;
- uses deterministic artifact identity by default and detects identity conflicts;
- rejects tampered parts, unknown versions/features, duplicate JSON keys, unlisted
  files, hash mismatches, timing violations, and physical schema drift.

The artifact reader follows the feature manifest, not recursive discovery. The engine
is intentionally not yet a collector stage, notebook cell, strategy API, backtester,
portfolio constructor, label/training pipeline, or ML model registry. It currently
publishes one security artifact per call; a dataset-wide orchestrator and feature
artifact catalog are future work.

The bounded engine documentation has been aligned without broadening its scope.

## Operational setup and current use

### Runtime prerequisites

- Docker Engine with Compose v2
- GNU Make
- outbound HTTPS access
- a real descriptive SEC User-Agent with contact email when SEC is enabled

The committed safe starter configuration is
[config/config.example.yaml](../config/config.example.yaml). Yahoo and FRED are
enabled by default; SEC, BCB, and CVM are disabled. `make setup` creates untracked,
private `.env` and `config/config.local.yaml` files and creates `data/raw`,
`data/normalized`, and `data/features`.

### Start and migrate

```sh
make setup
# set SEC_USER_AGENT in .env if SEC will be enabled
# edit config/config.local.yaml
make up
make migrate
make health
make urls
```

Fresh PostgreSQL volumes apply migrations `000001_core_metadata` through
`000004_run_inputs`, and `000005_nullable_macro_snapshot_value`. Existing initialized volumes use the idempotent `make migrate`
target. Current local runtime observations were PostgreSQL 17.10, Jupyter healthy,
Grafana healthy on `127.0.0.1:3001`, and PostgreSQL healthy on the default database
port. The configured host Grafana port is environment-dependent; use `make urls`.

### Collect

```sh
make ingest SOURCE=prices
make ingest SOURCE=sec
make ingest SOURCE=fred
make ingest SOURCE=bcb
make ingest SOURCE=cvm
```

The CVM command only publishes IPE rows for exact configured CVM codes. For a useful
CVM run, add the issuer's code to the local universe and choose explicit IPE years.
The default example has no CVM code and leaves CVM disabled.

Use a stable run key to test retry behavior:

```sh
make ingest SOURCE=prices RUN_KEY=my-retry-key
make ingest SOURCE=prices RUN_KEY=my-retry-key
```

The second successful attempt skips without fetching/writing the source again. A
partial or failed key is not reusable; choose a new key. `make rerun` generates and
reuses one key for an immediate retry demonstration.

If an orphan active run is confirmed, cancellation is explicit and reasoned:

```sh
docker compose --profile collect run --rm collector \
  --cancel-run --cancel-source fred \
  --cancel-run-key '<exact-run-key>' \
  --cancel-reason 'operator confirmed orphan after inspection'
```

At handoff time, one old `fred` run (`post-v1-acceptance-20260812/fred`) remained
`running` in PostgreSQL. It was not automatically cancelled; an operator should
inspect and cancel it only if confirmed orphaned.

### Research and feature use

The supported research path is:

1. Run collection and confirm raw manifests and canonical manifests exist.
2. Open or execute [python/notebooks/vertical_slice.ipynb](../python/notebooks/vertical_slice.ipynb).
3. Use `ResearchCatalog.research_snapshot(decision_at=..., macro_source=...)` for an explicit as-of
   price/fundamental/macro view.
4. Use `ResearchCatalog.point_in_time_inputs(...)` as the input boundary for a
   derived feature.
5. Use `publish_market_basic(...)` to create an immutable feature artifact under
   `data/features` and `read_feature_artifact(...)` to validate it later.

Run the notebook non-interactively with `make notebook`. It now inspects CVM
filings and an existing feature artifact in separate empty-safe sections; neither
section is joined into the one-to-one price/fundamental/macro snapshot.

### Monitoring

- Grafana is local-only by default. Use the provisioned pipeline-health and market
  dashboards.
- `make dashboard-smoke` validates dashboard JSON and runs PostgreSQL `EXPLAIN` for
  every dashboard query.
- Empty projections are valid and must remain visible as missing state.

## Verification evidence

Checks completed against implementation HEAD `742e5ae` or reported by the completed
implementation/acceptance workers:

- `CGO_ENABLED=0 go test -count=1 ./...` passed at implementation HEAD.
- `go vet ./...` passed at implementation HEAD.
- `git diff --check` passed and the implementation worktree was clean before these
  documentation files were added.
- `python3 schemas/validate_schemas.py` validated 13 JSON Schema documents.
- `python3 schemas/test_feature_schemas.py` passed feature fixtures and timing/fingerprint invariants.
- The feature worker reported 47 Python feature tests and Ruff passing in its test environment.
- Earlier catalog/dashboard Python checks passed in the project container; the current
  host shell does not have `pytest` installed, so `python3 -m pytest -q` on the host
  is not itself a passing verification command. Use `make test`, which installs the
  Python development dependencies inside the Jupyter container.
- PostgreSQL migrations have passed fresh apply, rollback, and existing-volume
  idempotent migration checks in the prior acceptance work.
- Empty-data notebook execution, fixture DuckDB readback, high-precision decimal
  preservation, provenance checks, strict dashboard JSON validation, and image builds
  passed in the earlier v0 acceptance workflow.
- The fresh official CVM IPE acceptance passed at
  `/home/luis/invs-acceptance/2026-08-12-cvm-ipe-current-Q8LklS`, including raw and
  canonical hashes, filing readback, provenance, exact availability-boundary checks,
  and identical-key retry behavior.
- The CAD parser/raw canary passed at
  `/home/luis/invs-acceptance/2026-08-12-cvm-cad-current-v3rGsD`; its partial status
  was the intentional raw-only publication boundary.

The operations slice at `0f73e39` additionally passed `go test ./...`, `go vet ./...`,
the collector image build, `make reconcile` against the running stack, and a
backup/restore drill that loaded PostgreSQL into a new `restore_*` database and
returned zero findings from reconciliation against the restored data root.

The retained post-metadata v0 r3 acceptance at
`/home/luis/invs-acceptance/2026-08-12-v0-r3` recorded:

| Source | Received | Retained normalized rows | Raw evidence |
| --- | ---: | ---: | ---: |
| SEC | 26,136 | 25,135 fundamentals | 2 objects |
| Yahoo | 1,661 | 1,661 prices | 1 object |
| FRED | 17,811 | 16,137 `DGS10` and 954 `CPIAUCSL` | 2 objects |
| BCB | 2,416 | 2,416 macro rows | 1 object |

The original acceptance verified successful identical-key retries, raw files,
manifest-backed Parquet, DuckDB readback, notebook execution, and populated Grafana
projections for that slice. It did not include CVM or the later feature engine.

## Known limitations and deliberately deferred work

The following are not accidental omissions:

- No full historical point-in-time guarantee for current Yahoo, FRED, or BCB pulls.
  Historical vintage providers and publication-time evidence are required before a
  serious backtest claim.
- The bounded ALFRED CPIAUCSL work package is live-accepted; broader v0.2 historical
  truth is not accepted.
- No broad B3/CVM market instrument discovery or integrated Brazilian market-data
  path. Yahoo `.SA` is the planned primary bridge, while selective B3 public datasets
  remain an enrichment and validation path; source terms, fixtures, mappings,
  coverage, and historical-availability policy are still pending.
- No canonical CVM CAD dataset or CAD snapshot table.
- No canonical SEC filing metadata dataset, despite SEC acceptance-time parsing.
- No fundamental or filing latest-only dashboard projection.
- No corporate-action collector despite the schema boundary existing.
- Current security-to-issuer mappings are current YAML configuration, not historical
  identity resolution.
- No distributed queue, scheduler, cloud object-store deployment, or production
  multi-user authorization.
- Feature engine is a closed first registry only; no feature discovery catalog,
  batch runner, calendar policy, strategy, backtester, portfolio, execution, labels,
  training data, or ML behavior.
- The roadmap is now present; version exit status must be updated there only after
  its stated acceptance gate passes.

## Exact next actions

Follow [the roadmap execution index](roadmap/README.md). The nearest cohesive units are:

1. Establish and observe the unattended daily runbook.
2. Close the remaining v0.1 acceptance gate, then continue v0.2 historical-truth
   work and its US/Brazil bias fixtures. Do not start strategy or execution work by
   treating current-vintage backfills as historical truth.
