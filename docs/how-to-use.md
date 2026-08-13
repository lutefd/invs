# How to use the current platform

This guide is for the current working v0/v1 foundation in this repository. It
covers operating the local runtime, collecting the configured sources, inspecting
the evidence, running point-in-time research, and publishing the first
deterministic market feature artifact.

The accepted v0 vertical slice is Yahoo daily prices, SEC company metadata and
facts, FRED series, and BCB SGS series. CVM is now also live-accepted at the
post-fix implementation head for the bounded IPE replay described below. CVM IPE
is canonical filing metadata; CVM CAD is currently raw, ingestion-only evidence.

This is research infrastructure. It is not a trading system, execution system,
portfolio accounting system, forecast engine, or backtester.

## 1. Prerequisites

Install or make available:

- Docker Engine with Docker Compose v2.
- Go, for local Go tests and development.
- Python 3.12 or newer for host-side research and tests.
- Internet access for provider collection.
- `jq` and `sha256sum` are useful for inspecting manifests, but are not required
  by the runtime.

The Docker runtime supplies PostgreSQL, the collector image, JupyterLab, DuckDB,
PyArrow, pandas, and the research package. Host-side Python users can install
the package and its development dependencies from `python/pyproject.toml`:

```sh
python3 -m venv .venv
. .venv/bin/activate
python -m pip install -e 'python[dev]'
```

Run all commands from the repository root:

```sh
cd /home/luis/dev/invs
```

## 2. First local setup

Create the local, untracked configuration files and data directories:

```sh
make setup
```

This creates `.env`, `config/config.local.yaml`, `data/raw/`,
`data/normalized/`, and `data/features/` if they do not already exist. It does
not overwrite an existing local configuration.

Edit `.env` and `config/config.local.yaml` before collecting. In particular:

- Set `SEC_USER_AGENT` in `.env` to a descriptive application name and contact
  address when SEC is enabled.
- Set `INVS_CONFIG_FILE` if the configuration lives somewhere other than
  `config/config.local.yaml`.
- Keep the `issuer_id` and `security_id` UUIDs stable for an existing entity.
- Put the exact security-to-issuer relationship in one `universe` entry.
- Add provider identifiers to that entry (`yahoo_symbol`, `cik`, or `cvm_code`)
  as appropriate.
- Configure only sources that are intended to run. The example enables Yahoo
  and FRED and disables SEC, BCB, and CVM by default.

Validate Compose interpolation and the YAML configuration before starting:

```sh
make config
```

Start the long-running services and apply migrations:

```sh
make up
make migrate
make health
make urls
```

`make up` starts PostgreSQL, JupyterLab, and Grafana. The collector is a batch
job and is intentionally not a long-running health-checked service; run it
with `make ingest` below. `make urls` prints the Jupyter URL and the configured
Grafana URL. Ports default to PostgreSQL `5432`, Jupyter `8888`, and Grafana
`3000`; `.env` can override them.

To stop containers while retaining named PostgreSQL and Grafana volumes:

```sh
make down
```

`make clean` also removes those named volumes. It does not remove the bind-
mounted `data/` directory, but it does remove operational database state, so
use it only when a fresh local PostgreSQL/Grafana state is intended.

## 3. Provider configuration and collection

The collector accepts these source names:

```sh
make ingest SOURCE=prices
make ingest SOURCE=sec
make ingest SOURCE=fred
make ingest SOURCE=alfred
make ingest SOURCE=bcb
make ingest SOURCE=cvm
make ingest SOURCE=all
```

`SOURCE=all` runs every provider enabled in the effective YAML configuration.
The collector always requires PostgreSQL for production collection because
source, security, and ingestion-run UUIDs are part of canonical provenance.

### Yahoo daily prices

Enable `providers.prices` and provide `yahoo_symbol` on each intended security:

```yaml
providers:
  prices:
    enabled: true
    start: 2020-01-01
universe:
  - issuer_id: 1b3d88f5-55b8-4dc5-a6be-2f77e9e99201
    security_id: 469fc20f-7d4b-45bb-b827-05f8410e71aa
    legal_name: Apple Inc.
    country_code: US
    security_type: common_stock
    primary_listing: true
    yahoo_symbol: AAPL
    currency: USD
```

Then run:

```sh
make ingest SOURCE=prices RUN_KEY=yahoo-aapl-2026-08-12
```

Symbols are URL-escaped by the adapter, including symbols such as `^BVSP` and
`BRK/B`. Yahoo's current download response is stored with the collector receipt
time as conservative availability evidence; the system does not infer the
historical public knowledge time of an old download.

### SEC company metadata and facts

Enable SEC and provide a CIK for the issuer. The adapter accepts quoted or
numeric CIK input, normalizes it to the SEC's ten-digit form, and requires the
descriptive user agent from `.env`:

```yaml
providers:
  sec:
    enabled: true
universe:
  - issuer_id: 1b3d88f5-55b8-4dc5-a6be-2f77e9e99201
    security_id: 469fc20f-7d4b-45bb-b827-05f8410e71aa
    legal_name: Apple Inc.
    country_code: US
    security_type: common_stock
    primary_listing: true
    cik: 320193
    yahoo_symbol: AAPL
    currency: USD
```

```sh
make ingest SOURCE=sec RUN_KEY=sec-aapl-2026-08-12
```

SEC filing metadata is parsed to establish acceptance timestamps where the
source provides them. The current canonical fundamental dataset contains SEC
facts; SEC filing metadata is not the same thing as the CVM `filings` dataset.

### FRED macro series

Enable FRED and list explicit series IDs:

```yaml
providers:
  fred:
    enabled: true
    series: [DGS10, CPIAUCSL]
```

```sh
make ingest SOURCE=fred RUN_KEY=fred-rates-inflation-2026-08-12
```

Set `FRED_API_KEY` in `.env` only when the selected FRED endpoint requires it.
Non-finite values are rejected. Current downloads receive conservative
ingestion availability, not a reconstructed historical vintage date. Use the
canonical macro revision and vintage fields when analyzing the series.

### ALFRED historical macro vintages

ALFRED uses the official [FRED series observations API](https://fred.stlouisfed.org/docs/api/fred/series_observations.html)
with `output_type=1`; the [ALFRED help](https://alfred.stlouisfed.org/help)
describes the archival vintage role. Configure
explicit semantic dimensions, a closed `realtime_end`, and optional observation
bounds. The adapter always requests from `1776-07-04` so the API cannot clip row
vintage starts to an operator-selected left boundary:

```yaml
providers:
  alfred:
    enabled: true
    series:
      - id: CPIAUCSL
        geography: US
        unit: index_1982_1984_100
        frequency: monthly
        seasonal_adjustment: seasonally_adjusted
        realtime_end: 2026-08-11
        observation_start: 2018-01-01
        observation_end: 2026-07-01
```

Set the 32-character lowercase alphanumeric `FRED_API_KEY` in `.env`, then run:

```sh
make ingest SOURCE=alfred RUN_KEY=alfred-cpi-2026-08-11
```

Every JSON page is stored before canonical publication and each row retains its
page hash. The row real-time start becomes date-precision `published_at` and
`vintage_at`; `available_at` is deliberately set 36 hours later because the API
does not document an intraday release time or timezone. Revisions are deterministic
zero-based ordinals per observation date. A source `.` is retained as an explicit
null vintage rather than dropped. ALFRED and current FRED remain separate sources.

### BCB SGS macro series

Enable BCB and configure each series with its code and semantic dimensions:

```yaml
providers:
  bcb:
    enabled: true
    series:
      - code: "432"
        geography: BR
        unit: percent
        frequency: daily
        seasonal_adjustment: not_adjusted
        start: 2020-01-01
        end: 2026-08-12
```

```sh
make ingest SOURCE=bcb RUN_KEY=bcb-432-2026-08-12
```

BCB is represented as canonical macro observations with revision-aware keys.
The source's explicit values and timestamps are retained; the collector does
not collapse a revision sequence such as A -> B -> A.

### CVM IPE filings and CAD

CVM IPE archives are global. Only rows whose `Codigo_CVM` exactly matches one
configured `universe[].cvm_code` are selected for an issuer. Rows for every
other company are ignored and counted explicitly as unconfigured, rather than
being treated as provider failures. Duplicate configured mappings are unsafe
and make the run fail/partial instead of guessing an issuer.

For canonical IPE filing metadata, use a bounded configuration such as:

```yaml
providers:
  cvm:
    enabled: true
    cad: false
    ipe:
      years: [2025, 2026]
universe:
  - issuer_id: 3b6f2f34-1f0e-4c39-8e68-35c53c1b9a10
    security_id: 60c2cc0f-3f5c-4a0a-a7d1-2e7f0b8d9c11
    legal_name: Example Brazilian Issuer
    country_code: BR
    security_type: common_stock
    primary_listing: true
    cvm_code: "9512"
    currency: BRL
```

```sh
make ingest SOURCE=cvm RUN_KEY=cvm-ipe-9512-2025-2026
```

The IPE adapter retains source fields and exact document URLs. Blank protocol
fields can use a validated numeric protocol/sequence/version identity derived
from the authoritative URL while preserving the original blank protocol in the
canonical row. IPE `available_at` is the durable receipt time of this
installation. `published_at` is intentionally unknown; `filing_date` is the
source delivery/reference date and is not a public publication timestamp.

CVM CAD is different. It is a current issuer-registration snapshot, not
versioned filing history. The collector stores the CAD response and parser
metadata under `data/raw/`, but currently publishes no CAD canonical rows and
no CAD dashboard projection. Enable it only when raw CAD evidence is wanted:

```yaml
providers:
  cvm:
    enabled: true
    cad: true
    ipe:
      years: [2026]
```

CAD non-publication is explicit in run metadata and can make the run partial;
use `cad: false` when testing a clean canonical IPE publication. The fresh
official IPE replay at implementation HEAD `742e5ae` passed: 30,232 rows were
received, 199 Petrobras rows were written, 0 were rejected, 29,934 unconfigured
rows were ignored, and 8 blank-protocol rows were retained through validated URL
identity. Its evidence archive is
`/home/luis/invs-acceptance/2026-08-12-cvm-ipe-current-Q8LklS`.

The separate CAD canary at
`/home/luis/invs-acceptance/2026-08-12-cvm-cad-current-v3rGsD` also passed raw
preservation and literal-quote parsing (2,677 rows, 0 shape errors), with the
expected `partial` status because CAD is intentionally not canonicalized.

## 4. Run keys, retries, and terminal states

Every source run has a logical key unique within its PostgreSQL data source.
Pass one explicitly when reproducibility or retry behavior matters:

```sh
make ingest SOURCE=fred RUN_KEY=fred-daily-2026-08-12
```

The equivalent direct command is useful when passing collector flags that are
not wrapped by Make:

```sh
docker compose --profile collect run --rm collector \
  --source fred --run-key fred-daily-2026-08-12
```

The same source and successful run key is idempotent: the collector recognizes
the terminal success and skips network fetch and publication. An active key
(`queued` or `running`) is not reused. A key that ended `partial`, `failed`, or
`cancelled` is not silently reused; choose a new explicit attempt key, for
example `fred-daily-2026-08-12-attempt-2`.

The state machine is:

```text
queued -> running -> succeeded
                  -> partial
                  -> failed
                  -> cancelled
```

- `succeeded`: accepted output and raw manifests were published and there were
  no rejected records or terminal errors.
- `partial`: some raw evidence or accepted output exists, but the requested
  scope had rejected records, a source/entity error, or an explicit
  ingestion-only boundary. Successful entities in a multi-entity partial run
  may still update latest-only price/macro projections.
- `failed`: the requested normalized dataset was not published. Raw evidence
  can still exist and should be inspected before retrying.
- `cancelled`: an operator explicitly cancelled a confirmed orphan active run;
  cancellation does not delete raw or canonical data.

Inspect the run ledger from PostgreSQL:

```sh
docker compose exec -T postgres sh -c \
  'psql -U "$POSTGRES_USER" -d "$POSTGRES_DB" -x -c \
  "SELECT s.code AS source, r.run_key, r.id, r.status, r.started_at,
          r.finished_at, r.records_received, r.records_written,
          r.records_rejected, r.raw_payload_count, r.raw_bytes,
          r.error_message, r.cursor
     FROM ingestion_runs r
     JOIN data_sources s ON s.id = r.data_source_id
    ORDER BY r.started_at DESC
    LIMIT 20"'
```

If a collector process disappears and the run is definitely orphaned, cancel
it with an explicit reason. Do not use cancellation as a normal retry shortcut:

```sh
docker compose --profile collect run --rm collector \
  --cancel-run \
  --cancel-source fred \
  --cancel-run-key fred-daily-2026-08-12 \
  --cancel-reason "confirmed orphan after operator inspection"
```

## 5. Storage and evidence inspection

The authoritative boundary is the combination of immutable raw bytes and
manifest-committed canonical Parquet. PostgreSQL and Grafana are operational
surfaces, not substitutes for canonical history.

### Raw responses

Raw objects and run manifests live below:

```text
data/raw/
  <source-specific object keys>
  runs/<source>/<ingestion-run-uuid>/manifest.json
  runs/<source>/<ingestion-run-uuid>/manifest.json.metadata.json
```

The run manifest records logical keys, object paths, SHA-256 hashes, sizes,
content types, fetch timestamps, and source attributes. Inspect it with:

```sh
find data/raw -type f | sort | less
jq . data/raw/runs/fred/<run-id>/manifest.json
sha256sum data/raw/<object-path>
```

Raw objects are immutable. If parsing or schema validation fails after bytes
were downloaded, the bytes should still be retained for diagnosis and replay.

### Canonical Parquet

Each canonical partition has a `manifest.json` and one or more immutable,
content-named parts:

```text
data/normalized/prices/source=yahoo/security_id=<security-uuid>/manifest.json
data/normalized/fundamentals/source=sec/issuer_id=<issuer-uuid>/manifest.json
data/normalized/macroeconomics/source=fred/series_id=<series-id>/manifest.json
data/normalized/macroeconomics/source=bcb/series_id=<series-id>/manifest.json
data/normalized/filings/source=cvm_ipe/issuer_id=<issuer-uuid>/manifest.json
```

The part path is `part-<sha256>.parquet`. A manifest contains schema version,
normalizer version, Git commit, source/run UUIDs, partition identity, row
count, and part hashes. Readers discover only manifests and only the parts
listed by those manifests. `data.parquet`, arbitrary unlisted parts, and
recursive Parquet globs are not canonical inputs.

Quick structural inspection:

```sh
jq . data/normalized/prices/source=yahoo/security_id=<security-uuid>/manifest.json
sha256sum data/normalized/prices/source=yahoo/security_id=<security-uuid>/part-*.parquet
```

Canonical v1 stores decimal values as exact UTF-8 strings. This avoids
ingestion-time rounding. The physical schema and row invariants are checked by
the Go writer and again by the Python/DuckDB catalog.

If the collector reports that normalized migration is required, stop and
archive the complete unmanaged normalized tree to an explicit recoverable
location before creating an empty `data/normalized/` and reingesting. Preserve
`data/raw/` and PostgreSQL metadata. Do not overwrite an old `data.parquet` or
invent missing source/run provenance in place.

## 6. DuckDB and Python research

The catalog is a read-only DuckDB session over manifest-backed canonical data.
On the host:

```python
from research import ResearchCatalog

catalog = ResearchCatalog("data").register()
for status in catalog.status():
    print(status)

latest = catalog.connection.execute(
    """
    SELECT security_id, trading_date, close_value, available_at,
           raw_payload_hash, ingestion_run_id
    FROM prices
    ORDER BY trading_date DESC
    LIMIT 10
    """
).fetchdf()
print(latest)
```

Run the same code in the Jupyter container with `/data`:

```sh
docker compose exec -T jupyter python - <<'PY'
from research import ResearchCatalog

catalog = ResearchCatalog("/data").register()
print(catalog.status())
print(catalog.connection.sql(
    "SELECT * FROM prices_canonical ORDER BY observed_at DESC LIMIT 5"
).fetchdf())
PY
```

The catalog exposes these canonical views:

- `prices_canonical`
- `fundamentals_canonical`
- `macroeconomics_canonical`
- `filings_canonical`

Canonical views preserve exact decimal strings, presence flags, timestamps, and
provenance. The shorter research views (`prices`, `fundamentals`,
`macroeconomics`, and `filings`) keep the exact string columns and add explicit
analysis projections. For example, prices have `close_value`,
`close_decimal`, and lossy `close`; fundamentals and macro values have
`value_text`, `value_decimal`, and lossy `value`. Use the string columns when
rounding would matter.

### Point-in-time snapshot

The joined snapshot API requires a decision timestamp and a configured
security-to-issuer mapping:

```python
from research import ResearchCatalog, load_security_mappings

catalog = ResearchCatalog("data").register()
mappings = load_security_mappings("config/config.local.yaml")
mapping = next(item for item in mappings if item.ticker == "AAPL")

snapshot = catalog.research_snapshot(
    decision_at="2026-08-12T21:00:00Z",
    mapping=mapping,
    fundamental_concept="RevenueFromContractWithCustomerExcludingAssessedTax",
    macro_source="fred",
    macro_series_id="DGS10",
    start="2024-01-01",
    end="2026-08-12",
)
print(snapshot.tail())
```

The query selects only price, fundamental, and macro facts whose conservative
`available_at` (and required observation time) is no later than the explicit
decision time. `published_at` is retained source metadata; it is not silently
used as a substitute for `available_at`. The configured mapping is a current
configuration input, not a historical identity-resolution claim.

### Exact point-in-time feature inputs

For deterministic feature work, use the narrower API that returns selected
price rows and their input lineage:

```python
from research import ResearchCatalog

catalog = ResearchCatalog("data").register()
inputs = catalog.point_in_time_inputs(
    decision_at="2026-08-12T21:00:00Z",
    security_id="469fc20f-7d4b-45bb-b827-05f8410e71aa",
)
print(inputs.frame[[
    "trading_date", "close_value", "observed_at", "available_at",
    "raw_payload_hash", "manifest_path", "part_sha256",
]])
print(inputs.max_available_at)
```

`point_in_time_inputs` supports `dataset="prices"` only at present and fails
closed for other datasets. It never forward-fills missing bars, chooses a
future observation, or discovers an unlisted Parquet part.

### Filing catalog and as-of policies

CVM IPE metadata is queried separately from the price/fundamental/macro
snapshot:

```python
catalog = ResearchCatalog("data").register()

filings = catalog.connection.sql(
    """
    SELECT source, issuer_id, filing_date, form_type, subject,
           document_url, published_at, available_at, source_document_id
    FROM filings
    WHERE source = 'cvm_ipe'
    ORDER BY filing_date DESC
    LIMIT 20
    """
).fetchdf()

known = catalog.filings_as_of(
    decision_at="2026-08-12T21:00:00Z",
    mode="historical",
    issuer_id="3b6f2f34-1f0e-4c39-8e68-35c53c1b9a10",
    source="cvm_ipe",
)
```

`filings_as_of` always applies the explicit `available_at` cutoff and, where
present, the `observed_at` cutoff. `mode="historical"` excludes CVM CAD
identities. `installation_replay` and `known_at_installation` are explicit
local replay policies; they do not turn a current CAD snapshot into historical
issuer state. Since CAD is currently raw-only, it is not a canonical filing
row to query today.

## 7. Publishing and validating market-basic features

The first feature registry is `market-basic` version `1.0.0`. It currently
computes four exact-string-or-null fields for one security as of one decision
timestamp:

- `close`: the latest eligible close;
- `return_1d`: the exact-decimal return from the prior eligible close, or null;
- `range_1d`: `high - low`, or null when the required input is unavailable;
- `volume`: the latest volume, or null when the price bar has no volume.

There is no forward fill, `DOUBLE` calculation, strategy, signal, or model
hidden behind this registry. The supported operator command requires an explicit
security and decision timestamp:

```sh
make feature \
  SECURITY_ID=469fc20f-7d4b-45bb-b827-05f8410e71aa \
  DECISION_AT=2026-08-12T21:00:00Z \
  FEATURE_DELAY=30
```

The command publishes through the Jupyter image, validates the result immediately,
and prints a compact JSON summary containing its manifest path, lineage fingerprint,
timing boundary, and exact feature values. Repeating the identical command is
idempotent. Validate an existing container path independently with:

```sh
make feature-validate \
  FEATURE_MANIFEST=/data/features/market-basic/1.0.0/artifact-<uuid>/manifest.json
```

For library use, publish directly from Python:

```python
from research import ResearchCatalog
from research.features import (
    publish_market_basic,
    read_feature_artifact,
    validate_feature_artifact,
)

catalog = ResearchCatalog("data").register()
manifest_path = publish_market_basic(
    catalog,
    decision_at="2026-08-12T21:00:00Z",
    security_id="469fc20f-7d4b-45bb-b827-05f8410e71aa",
    features_root="data/features",
    computation_delay_seconds=30,
    git_commit="unknown",  # or a full lower-case 40-character Git SHA
)

artifact = read_feature_artifact(manifest_path)
print(artifact.manifest)
print(artifact.observations)
validate_feature_artifact(manifest_path)
```

In the container, use `/data/features` and a catalog rooted at `/data`. The
artifact is stored at:

```text
data/features/market-basic/1.0.0/artifact-<artifact-uuid>/manifest.json
data/features/market-basic/1.0.0/artifact-<artifact-uuid>/part-<sha256>.parquet
```

The manifest records the decision time, maximum input availability, computation
delay, derived feature availability, selected input manifests and parts, input
fingerprint, generator version, and Git commit. The artifact identity is
deterministic for the feature set, version, security, and decision time by
default. Re-publishing identical content is idempotent; attempting to reuse an
identity with changed content raises a conflict. Validation rejects hash
mismatches, unsupported versions, malformed decimal strings, wrong physical
types, duplicate JSON keys, missing listed parts, and unlisted files.

## 8. Notebook and Grafana

Execute the empty-safe vertical-slice notebook in a disposable Jupyter process:

```sh
make notebook
```

Or open the running JupyterLab instance at the URL printed by:

```sh
make urls
```

The notebook demonstrates the price/SEC fundamentals/FRED point-in-time join,
then inspects CVM filings and an existing `market-basic` feature artifact in
separate optional sections. It intentionally never joins CVM filings one-to-many
into the snapshot. Use `EXAMPLE_FILING_MODE=installation_replay` for an explicit
installation-time filing view, and set `EXAMPLE_FEATURE_MANIFEST` to inspect a
specific feature manifest; both sections remain empty-safe when no artifact or
filing dataset is present.

Grafana is available at the configured local port. The provisioned dashboards
are:

- `pipeline-health`: run statuses, errors, counts, raw evidence, and coverage;
- `market-overview`: configured securities and latest Yahoo price/FRED macro
  projections.

The dashboards query PostgreSQL's replaceable latest-only projections. They do
not replace canonical Parquet history, and they do not synthesize a missing
snapshot. A configured security with no published projection is shown as
`no snapshot published`. SEC is ingestion-oriented in the dashboard, and CVM
filings/CAD are not current price/macro snapshot tables.

Run dashboard checks locally and against PostgreSQL:

```sh
make dashboard-smoke
```

The smoke check rejects duplicate JSON keys and emits `EXPLAIN` statements for
each dashboard query.

## 9. Reconciliation, backup, and restore

Use the read-only reconciliation report before and after operational work:

```sh
make reconcile
```

It checks active ingestion runs, raw manifest/object hashes, normalized manifest
and part integrity, unlisted Parquet files, and feature lineage. Findings are
reported for operator action; the command never cancels runs or deletes evidence.
The complete backup, clean-root restore, and host-level daily schedule are in
[the recovery runbook](operations-recovery.md).

```sh
make backup BACKUP_DIR=/path/to/new/backup
make restore BACKUP_DIR=/path/to/backup RESTORE_DIR=/tmp/invs-restore RESTORE_DB=restore_invs
```

The restore command refuses existing destinations and only creates a database
whose name starts with `restore_`, so the application database is not replaced.

## 10. Safety rules for research

1. Treat raw bytes and committed canonical manifests as the evidence boundary.
   PostgreSQL projections may be rebuilt; raw and canonical files should not be
   overwritten in place.
2. Use an explicit `decision_at` for any historical question. A row is eligible
   only when its conservative `available_at` is no later than that timestamp.
3. Do not replace missing `available_at` with `filing_date`, `period_end`,
   trading date, or `published_at` by intuition.
4. Preserve exact decimal strings through ingestion and feature publication.
   `DECIMAL(38,18)` and `DOUBLE` are analysis conveniences with explicit loss
   boundaries, not canonical values.
5. Do not forward-fill missing observations or infer a price, volume, filing,
   or macro value from a neighboring row.
6. A current YAML universe mapping is not a historical security-master
   resolution. Do not use it to assert that an issuer/security relationship was
   valid at every past decision time.
7. CVM IPE receipt availability supports an installation replay. It does not
   prove when the document was publicly knowable. CVM CAD is current
   registration data, not historical issuer state.
8. Read only manifest-listed Parquet parts. A stray file in a partition is not
   automatically part of the dataset.

## 11. Troubleshooting

### PostgreSQL or migrations are unavailable

Check the service and database health, then apply migrations:

```sh
make up
make migrate
make health
```

If `DATABASE_URL is required for canonical collection` appears, check that
`.env` is present and that the Compose service can resolve the PostgreSQL
container. The collector cannot safely mint source/run provenance without the
metadata database.

### The collector refuses to start because of normalized data

This is a deliberate fail-closed response. Inspect the tree first:

```sh
find data/normalized -maxdepth 5 -type f -print | sort
```

If it contains unmanaged pre-contract or pre-manifest files, move the complete
normalized tree to a named, recoverable archive, create a fresh normalized tree,
and reingest from the preserved raw evidence/provider sources. Do not delete
the raw tree and do not rewrite an old Parquet file to pretend it has missing
source/run lineage.

### A retry says the run key is already finished

Read the status from `ingestion_runs`. A successful key is intentionally a no-op;
for a `partial`, `failed`, or `cancelled` run, use a new key with an explicit
attempt suffix. This preserves the original terminal evidence and makes the
retry auditable.

### A run is partial but has useful rows

Inspect `error_message`, `cursor`, raw manifests, and the per-entity counts.
Partial runs preserve accepted raw/canonical output and can publish successful
price/macro entities while withholding a failed entity's projection. Correct
the provider/configuration issue and retry under a new key.

### No rows appear in a research view

Check `catalog.status()` and confirm that the source is enabled, the intended
identifier is present in `universe`, and the provider actually wrote a
manifest. For CVM, confirm an exact `cvm_code`; global IPE rows for unconfigured
issuers are intentionally ignored. For an as-of query, move `decision_at`
forward only when that reflects the question—do not bypass the cutoff.

### A feature publication fails

Confirm that `point_in_time_inputs` returns eligible rows and that every
selected manifest/part still matches its SHA-256. Use a new decision time or a
new artifact identity only when the research question changed. If the same
artifact identity conflicts, investigate the input or generator change instead
of overwriting the artifact directory.

### Grafana shows no snapshot

This is a meaningful operational state. Confirm that the relevant price/macro
run succeeded and that its accepted candidate passed PostgreSQL finalization.
Canonical Parquet can contain history even when a replaceable latest projection
is absent. CVM filings and CAD do not populate the price/macro snapshot tables.

## 12. What this version can and cannot answer

### It can answer

- What raw response bytes were collected, when they were fetched, and which
  SHA-256 identifies them.
- Which canonical Yahoo price, SEC fact, FRED revision, or BCB observation was
  selected by an explicit installation-time knowledge cutoff.
- Which exact canonical Parquet part, manifest, source, run, and raw locator
  support a selected observation.
- Which configured security/issuer pair is used for the current research run.
- Which CVM IPE filing metadata rows were received by this installation and are
  eligible under `filings_as_of`.
- A deterministic `market-basic` artifact with exact decimal/null outputs and
  reproducible input lineage.
- Current latest-only operational coverage and projection health in Grafana.

### It cannot honestly answer yet

- A historically accurate public-availability timestamp for a current Yahoo or
  FRED download when the source did not provide one.
- A historically accurate public-availability timestamp for CVM IPE; its
  current contract is receipt-time installation replay.
- Historical issuer state from CVM CAD; CAD is current and raw-only.
- Full document contents, extracted filing statements, or a canonical CAD
  history; the current CVM slice is filing metadata plus raw source evidence.
- A complete B3/Brazilian market-data universe; Yahoo Finance `.SA` is the planned
  primary bridge, but integration, coverage, and historical-fitness acceptance remain
  pending.
- A backtest, strategy signal, portfolio, forecast, ML model, execution order,
  or performance claim. The feature engine is deliberately only the first
  deterministic market-basic registry.
- A historical identity relationship solely from today's YAML universe mapping.
- A latest fundamental snapshot in PostgreSQL; canonical fundamentals remain in
  Parquet, while PostgreSQL latest-only projections currently cover prices and
  macro observations.

For the current readiness boundary, start with
[`README.md`](../README.md), [`docs/architecture.md`](architecture.md), and the
ADRs, then use this guide as the operator/researcher runbook.
