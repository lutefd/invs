# How to use the current platform

This guide is for the current working v0/v1 foundation in this repository. It
covers operating the local runtime, collecting the configured sources, inspecting
the evidence, running point-in-time research, and publishing deterministic market
feature artifacts and batches.

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

To verify the v0.2 historical metadata boundary against PostgreSQL, run
`make historical-truth-db-test`. It uses isolated fixtures and a disposable fresh
database; it does not admit live security-master or calendar data.

To publish or revalidate a complete bounded point-in-time bias audit, use a strict
specification whose evidence paths and SHA-256 values are already pinned:

```sh
make bias-audit AUDIT_SPEC=/absolute/path/spec.json AUDITS_ROOT=/absolute/path/artifacts
make bias-audit-validate AUDIT_MANIFEST=/absolute/path/artifacts/artifact_id=UUID/manifest.json
```

The audit command is dependency-free on the host. It refuses missing US/Brazil
categories, non-exact before/at/after clocks, current-only or installation-replay
inputs relabelled as historically safe, unsupported actions claimed eligible, and
changed evidence bytes. The accepted v0.2 example and fitness decisions are in the
[bias-audit report](acceptance/2026-08-24-v0.2-point-in-time-bias-audit.md).

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
make ingest SOURCE=b3
make ingest SOURCE=b3-calendar
make ingest SOURCE=nyse
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
historical public knowledge time of an old download. Yahoo chart OHLC and volume are
`split_adjusted`, not raw corporate-action inputs. Legacy Yahoo manifests carrying
the former `raw` label fail closed and must be archived and reingested; never rewrite
the immutable manifest in place.

### SEC company metadata, facts, and filing catalog

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

The same raw-first run publishes SEC facts to `fundamentals/source=sec/...` and SEC
submissions metadata to `filings/source=sec/...`. Filing identity is the accession
number. Exact EDGAR acceptance is `published_at` and `available_at`; `filing_date`
and optional source `reportDate` remain separate civil dates and never substitute
for availability. Safe nested primary-document paths are preserved in the archive
URL. The submissions feed does not state an exact amended accession for every
amendment, so `amends_source_document_id` remains empty rather than being inferred.
See the [SEC filing acceptance report](acceptance/2026-08-24-sec-filing-publication.md).

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

### BCB PTAX USD/BRL FX

PTAX is a separate provider from BCB SGS. Enable it with an explicit inclusive
range of no more than 366 days:

```yaml
providers:
  ptax:
    enabled: true
    start: 2026-08-10
    end: 2026-08-14
```

```sh
make ingest SOURCE=ptax RUN_KEY=ptax-usd-brl-2026-08-10-14
```

The collector retains the official OData response before parsing and publishes
manifest-backed Parquet under
`data/normalized/fx/source=bcb_ptax/pair=USD-BRL/`. Each row preserves both PTAX
buy and sell rates, the exact source bulletin timestamp, and the later local receipt.
A changed rate at the same source key blocks publication because the source does not
expose enough correction chronology to infer a revision safely.
The retained live proof, exact hashes, decision boundary, and pinned calculations are
in the [PTAX FX acceptance report](acceptance/2026-08-24-ptax-fx.md).

### B3 COTAHIST historical-price bridge

Use this provider for a bounded, official Brazil price archive without Yahoo. Only
closed annual files are accepted, and each ticker must resolve to one exact
BR/B3/BVMF/BRL universe row with an ISIN:

```yaml
providers:
  b3_historical_prices:
    enabled: true
    year: 2021
    start: 2021-09-06
    end: 2021-09-10
    tickers: [PETZ3]
```

```sh
make ingest SOURCE=b3-prices RUN_KEY=b3-cotahist-petz3-2021-09-06-10
```

The complete annual ZIP is retained before parsing. Canonical output is written to
`data/normalized/prices/source=b3_cotahist/security_id=<security-uuid>/` with raw
price basis, date observation precision, no invented publication timestamp, and
receipt-time availability. It is installation-replay only: an earlier historical
decision must return no rows. See the
[B3 COTAHIST acceptance report](acceptance/2026-08-24-b3-cotahist-price-bridge.md).

### B3 public instrument identity/listing snapshot

B3 collection is deliberately exact and bounded. Add the security to the universe
with its stable internal IDs and explicit B3 mapping, including the B3 ISIN, then
request one known report date and a ticker allowlist:

```yaml
providers:
  b3:
    enabled: true
    report_date: 2026-08-21
    tickers: [PETR4]
universe:
  - issuer_id: 3b6f2f34-1f0e-4c39-8e68-35c53c1b9a10
    security_id: 60c2cc0f-3f5c-4a0a-a7d1-2e7f0b8d9c11
    legal_name: Example Brazilian Issuer
    country_code: BR
    security_type: common_stock
    primary_listing: true
    cik: 1
    ticker: PETR4
    isin: BRPETRACNPR6
    identifier_valid_from: 2020-01-01
    exchange: B3
    mic: BVMF
    currency: BRL
```

Every requested ticker must match exactly one universe row with the declared
`BR`/`B3`/`BVMF`/`BRL` mapping and matching ISIN. The collector stores the complete
CSV before publishing BVMF-scoped ticker and listing versions. `TradgStartDt` and
`TradgEndDt` are B3's source-defined trading interval; receipt time is used for
`available_at`. This path does not infer universe membership, original legal
listing dates, delistings, or corporate actions. See the [B3 acceptance report](acceptance/2026-08-22-b3-instruments-security-master.md).

```sh
make ingest SOURCE=b3 RUN_KEY=b3-instruments-2026-08-21
```

### Versioned B3 and NYSE calendars

Calendar collection publishes an append-only manifest plus one explicit session
row for every local date in the requested coverage. Closed weekends and holidays
are rows, not gaps. Configure a deliberately bounded B3 interval and an explicit
NYSE year:

```yaml
providers:
  b3:
    enabled: true
    report_date: 2026-08-21
    tickers: [PETR4]
    calendar:
      enabled: true
      year: 2026
      coverage_start: 2026-08-24
      coverage_end: 2026-08-28
  nyse:
    enabled: true
    year: 2026
    coverage_start: 2026-01-01
    coverage_end: 2026-12-31
```

Run B3 instrument and calendar collection together, the B3 calendar alone, or the
NYSE calendar alone:

```sh
make ingest SOURCE=b3 RUN_KEY=b3-snapshot-and-calendar-2026-08-24
make ingest SOURCE=b3-calendar RUN_KEY=b3-calendar-2026-08-24
make ingest SOURCE=nyse RUN_KEY=nyse-calendar-2026
```

`SOURCE=b3-calendar` uses the `b3` data source but an independent calendar run-key
scope, so it cannot collide with a B3 instrument run using the same operator key.
Exact-key retries return the successful existing run.

The compiler uses `America/Sao_Paulo` for BVMF and `America/New_York` for XNYS,
applies source-declared closures and special hours, and fingerprints the complete
covered row set. The official HTML pages are current/reference evidence without an
exposed historical publication or correction sequence. Every resulting version is
therefore eligible only at its local raw receipt time; do not use it to simulate an
earlier decision. Keep B3 coverage narrow unless retained official evidence supports
the regular-hours effective interval. See the
[exchange-calendar publication report](acceptance/2026-08-23-exchange-calendar-publication.md)
for the accepted boundary.

For admitted historical artifacts, configure exact resource URLs, lowercase
SHA-256 hashes, conservative `available_at` timestamps, and declarative exception
rows under `providers.nasdaq_calendar_history` or
`providers.b3_calendar_history`. Then run the source-specific collector:

```sh
make ingest SOURCE=nasdaq-calendar-history RUN_KEY=nasdaq-calendar-history-2024
make ingest SOURCE=b3-calendar-history RUN_KEY=b3-calendar-history-2026
```

These selectors publish full immutable versions to the separate
`nasdaq_calendar` and `b3_calendar` data sources. Never substitute a current page,
an unpinned download, or an inferred weekday calendar. Select the manifest with
`available_at <= decision_at`, use its exact `calendar_version` for session
resolution, and carry the resulting pin into feature publication. See the
[historical calendar acceptance report](acceptance/2026-08-24-historical-calendar-publication.md).

### Corporate actions and adjusted-price artifacts

Exact historical actions are disabled by default. Configure every SEC or B3
resource with its exact URL, lowercase SHA-256, content type, declared availability
policy, and source-located action transcription under `providers.sec_action_history`
or `providers.b3_action_replay`. Then collect with an independent run-key scope:

```sh
make ingest SOURCE=sec-actions RUN_KEY=sec-actions-<bounded-slice>
make ingest SOURCE=b3-action-replay RUN_KEY=b3-action-replay-<bounded-slice>
```

SEC action versions become knowable at the exact configured EDGAR acceptance time.
B3 public sample versions become knowable only at local receipt. Unknown B3 action
states remain `unsupported`; do not map a source code to `active` without admitted
source semantics.

Export one resolver-backed action snapshot to a new host file. Obtain the explicit
source UUID from `data_sources`; do not select a source implicitly by name inside
research code.

```sh
mkdir -p data/action-snapshots
make action-snapshot \
  DATA_SOURCE_ID=<action-source-uuid> \
  SECURITY_ID=<security-uuid> \
  DECISION_AT=2020-09-02T22:00:00Z \
  ACTIONS_FILE=data/action-snapshots/aapl-2020-09-02.json
```

The target refuses to overwrite an existing snapshot. It includes the exact source,
security, decision time, and latest knowable revision of every event family;
cancellations are omitted while unsupported latest states remain present to block
downstream adjustment.

Publish only from a manifest whose rows are truly `price_basis=raw`. Host `data/`
is mounted at `/data` in the research container, so command inputs use container
paths:

```sh
make adjust \
  RAW_PRICE_MANIFEST=/data/normalized/prices/source=<raw-source>/security_id=<security-uuid>/manifest.json \
  ACTIONS_FILE=/data/action-snapshots/aapl-2020-09-02.json \
  DECISION_AT=2020-09-02T22:00:00Z

make adjust-validate \
  ADJUSTMENT_MANIFEST=/data/adjusted/prices/artifact_id=<artifact-uuid>/manifest.json
```

`backward_split_dividend_1_0_0` accepts splits, reverse splits, and cash dividends.
It pins the raw manifest and part, the ordered canonical action snapshot, and every
output part. Other action types, unsupported states, missing economics, currency
mismatches, future knowledge, and legacy Yahoo `raw` rows produce no artifact. See
the [corporate-action acceptance report](acceptance/2026-08-24-corporate-action-publication.md).

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

## 7. Publishing and validating deterministic market features

The first feature registry is `market-basic` version `1.0.0`. It currently
computes four exact-string-or-null fields for one security as of one decision
timestamp:

- `close`: the latest eligible close;
- `return_1d`: the exact-decimal return from the prior eligible close, or null;
- `range_1d`: `high - low`, or null when the required input is unavailable;
- `volume`: the latest volume, or null when the price bar has no volume.

There is no forward fill, `DOUBLE` calculation, strategy, signal, or model
hidden behind this registry. Artifact contract `1.1.0` also requires an exact
calendar pin selected from the durable calendar manifest resolver at the same
decision time. Save that resolver result as a JSON object such as:

```json
{
  "data_source_id": "<calendar data-source UUID>",
  "mic": "XNAS",
  "calendar_version": "<immutable calendar version>",
  "session_fingerprint": "<64 lowercase hex>",
  "calendar_available_at": "<canonical UTC timestamp>",
  "decision_clock_policy": "after_close_next_session"
}
```

The supported operator command requires the security, decision timestamp, and
that file explicitly:

```sh
make feature \
  SECURITY_ID=469fc20f-7d4b-45bb-b827-05f8410e71aa \
  DECISION_AT=2026-08-12T21:00:00Z \
  CALENDAR_PIN=/data/calendar-pins/xnas.json \
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
    calendar_pin={
        "data_source_id": "<calendar data-source UUID>",
        "mic": "XNAS",
        "calendar_version": "<immutable calendar version>",
        "session_fingerprint": "<64 lowercase hex>",
        "calendar_available_at": "<canonical UTC timestamp>",
        "decision_clock_policy": "after_close_next_session",
    },
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

The manifest records the calendar pin and decision-clock policy, decision time, maximum input availability, computation
delay, derived feature availability, selected input manifests and parts, input
fingerprint, generator version, and Git commit. The artifact identity is
deterministic for the feature set, artifact version, security, decision time, and
complete input fingerprint by default. A price-lineage or calendar-pin change
therefore forks identity. Re-publishing identical content is idempotent; attempting to reuse an
identity with changed content raises a conflict. Validation rejects hash
mismatches, unsupported versions, malformed decimal strings, wrong physical
types, duplicate JSON keys, missing listed parts, and unlisted files.

### Publishing market-momentum

The second registry entry is `market-momentum` version `1.0.0`, defined in
[ADR 0012](adr/0012-market-momentum-feature-set.md) and backed by the strict
[`feature-momentum-manifest.schema.json`](../schemas/feature-momentum-manifest.schema.json)
and [`feature-momentum-observation.schema.json`](../schemas/feature-momentum-observation.schema.json)
contracts. It consumes one point-in-time daily price series and publishes:

- `return_1m`, `return_3m`, `return_6m`, and `return_12m`, using 21, 63, 126,
  and 252 eligible observations respectively;
- `realized_volatility_1m`, the annualized sample volatility of the trailing 21
  one-observation returns; and
- `max_drawdown_1m`, the minimum running close-to-peak return across the trailing
  21 eligible closes.

Each output is an exact decimal string or typed null until its own prerequisites
exist. The full family requires 253 closes. A zero denominator or invalid price
series rejects the partition. Like `market-basic`, its receipt-time price input is
labelled `installation_replay_only`; this feature family does not make a historical
public-availability claim.

The same operator target selects the family explicitly:

```sh
make feature \
  SECURITY_ID=469fc20f-7d4b-45bb-b827-05f8410e71aa \
  DECISION_AT=2026-08-12T21:00:00Z \
  CALENDAR_PIN=/data/calendar-pins/xnas.json \
  FEATURE_SET=market-momentum \
  FEATURE_SET_VERSION=1.0.0
```

For library use, call `publish_market_momentum` from
`research.market_momentum`; the generic `read_feature_artifact` and
`validate_feature_artifact` functions dispatch to its strict reader based on the
manifest feature-set identity.

### Publishing fundamental and macro features

`fundamental-growth` 1.0.0 uses exact reviewed SEC mappings from
[`feature-taxonomy-registry.json`](../schemas/feature-taxonomy-registry.json). A
security-to-issuer mapping is mandatory; the producer never substitutes a current
YAML mapping or treats an unmapped fact as zero. Its outputs are `revenue`,
`revenue_growth_yoy`, and `operating_margin`.

`macro-state` 1.0.0 uses an explicit ALFRED historical-vintage selector. Pass the
source, series, geography, unit, and frequency when publishing a macro artifact:

```sh
make feature \
  SECURITY_ID=469fc20f-7d4b-45bb-b827-05f8410e71aa \
  ISSUER_ID=1b3d88f5-55b8-4dc5-a6be-2f77e9e99201 \
  DECISION_AT=2025-02-01T00:00:00Z \
  CALENDAR_PIN=/absolute/path/to/xnys-calendar-pin.json \
  FEATURE_SET=fundamental-growth \
  TAXONOMY_REGISTRY=/absolute/path/to/feature-taxonomy-registry.json

make feature \
  SECURITY_ID=469fc20f-7d4b-45bb-b827-05f8410e71aa \
  DECISION_AT=2025-02-01T00:00:00Z \
  CALENDAR_PIN=/absolute/path/to/xnys-calendar-pin.json \
  FEATURE_SET=macro-state \
  MACRO_SOURCE=alfred \
  MACRO_SERIES_ID=CPIAUCSL \
  MACRO_GEOGRAPHY=US \
  MACRO_UNIT=Index \
  MACRO_FREQUENCY=monthly
```

For a fundamental batch, provide both a strict mapping file and the reviewed
taxonomy registry:

```json
{"mappings": [{"security_id": "469fc20f-7d4b-45bb-b827-05f8410e71aa", "issuer_id": "1b3d88f5-55b8-4dc5-a6be-2f77e9e99201"}]}
```

```sh
make feature-batch \
  FEATURE_REGISTRY=/absolute/path/to/schemas/feature-set-registry.json \
  BATCH_UNIVERSE=/absolute/path/to/universe.json \
  BATCH_SCHEDULE=/absolute/path/to/schedule.json \
  CALENDAR_PIN=/absolute/path/to/xnys-calendar-pin.json \
  FEATURE_SET=fundamental-growth \
  SECURITY_MAPPINGS=/absolute/path/to/security-mappings.json \
  TAXONOMY_REGISTRY=/absolute/path/to/feature-taxonomy-registry.json
```

For a macro batch, use the same batch target with `FEATURE_SET=macro-state` and the
five `MACRO_*` selectors shown above. The macro producer selects eligible historical
vintages at each decision time, so a later revision can change a later feature row
without changing an earlier one.

### Inspecting feature-level quality

The catalog report and the feature-quality report serve different purposes. After a
batch is published, run the read-only quality report against the host paths for the
batch manifest and registry:

```sh
make feature-quality-report \
  BATCH_MANIFEST=/absolute/path/to/data/features/batches/market-basic/1.0.0/batch-<uuid>/manifest.json \
  FEATURE_REGISTRY=/absolute/path/to/schemas/feature-set-registry.json \
  STALE_AFTER_SECONDS=2592000
```

The report is validated as `feature-quality-report.schema.json` and includes coverage
by decision and feature, present/null counts, registry-approved null reasons, stale
input ages, explicit rejected partitions, source contribution, selected canonical
manifest/part hashes, and raw record locators. It applies the input decision clock
before analyzing rows, including period-end and macro-vintage cutoffs. It emits no
feature values and writes neither PostgreSQL nor the feature root. A hash mismatch or
unsafe lineage path fails closed.

### Publishing a dataset-level batch

The controlled registry is the checked-in
`schemas/feature-set-registry.json`. It currently contains `market-basic`,
`market-momentum`, `fundamental-growth`, and `macro-state`, each at version
`1.0.0`. Resolve feature sets by exact name and version;
do not pass arbitrary Python functions or a latest-only PostgreSQL projection to a
batch. The current registry explicitly labels receipt-time price inputs
`installation_replay_only`; a batch does not upgrade that label into a historical
public-availability claim. The batch target defaults to `market-basic`; pass
the exact `FEATURE_SET` and `FEATURE_SET_VERSION` to select one of the other
registered producers.

Create an explicit universe snapshot and decision schedule. Both files are strict
JSON objects:

```json
{"security_ids": ["469fc20f-7d4b-45bb-b827-05f8410e71aa"]}
```

```json
{"decision_ats": ["2026-08-12T21:00:00Z", "2026-08-13T21:00:00Z"]}
```

Publish and immediately validate the batch through the operator path:

```sh
make feature-batch \
  FEATURE_REGISTRY=/absolute/path/to/schemas/feature-set-registry.json \
  BATCH_UNIVERSE=/absolute/path/to/universe.json \
  BATCH_SCHEDULE=/absolute/path/to/schedule.json \
  CALENDAR_PIN=/absolute/path/to/xnas-calendar-pin.json
```

The batch command partitions by decision timestamp and security, publishes each
partition as an immutable child artifact for the selected feature set, and installs
one batch manifest only after the children validate. Missing input partitions are
retained in the manifest's `rejected` list with a reason and detail. Repeating the same command
with the same normalized manifests, registry, universe, schedule, and calendar
reuses the child artifacts and returns the same batch manifest; a changed input or
registry fingerprint creates a different batch identity.

Validate a batch independently from inside the Jupyter container:

```sh
make feature-batch-validate \
  FEATURE_REGISTRY=/absolute/path/to/schemas/feature-set-registry.json \
  BATCH_MANIFEST=/data/features/batches/<feature-set>/<version>/batch-<uuid>/manifest.json
```

The batch manifest is stored under
`data/features/batches/<feature-set>/<version>/batch-<uuid>/manifest.json`. It records
the registry hash, exact universe fingerprint, sorted decision schedule, calendar
pin, input fitness, selected input manifest/part hashes, child output part hashes,
row count, and accepted/rejected partition summary. Feature rows remain in the
child Parquet artifacts; PostgreSQL is not used as a feature-row store.

### Cataloging a validated dataset-level batch

Register a completed batch after its manifest and child parts have been validated:

```sh
make feature-catalog \
  BATCH_MANIFEST=/data/features/batches/market-basic/1.0.0/batch-<uuid>/manifest.json
```

The target runs `feature-batch-validate` inside Jupyter, applies the same strict
filesystem checks in the collector image, and then writes the catalog registration to
PostgreSQL. It stores the batch identity, registry and input fingerprints, calendar
pin, decision points, universe members, input-fitness labels, and accepted child
manifest/part pointers. Feature values remain in Parquet. The path is relative to
`/data/features` inside Compose, and the database URL comes from the configured
Compose environment; do not pass a host-only path to the container.

Registration is idempotent: repeating the command for the same immutable batch returns
`already_present: true`. A changed registration under the same batch identity fails as
a conflict. The catalog boundary is dataset-level and published-status only; it does
not provide feature-row queries or automatic orphan repair from PostgreSQL.

### Reading catalog coverage and lineage

Inspect all registered feature batches in text form:

```sh
make feature-report
```

Apply the usual feature-set filters and request the machine-readable report when
needed:

```sh
make feature-report \
  FEATURE_SET=market-basic \
  FEATURE_SET_VERSION=1.0.0 \
  FEATURE_REPORT_JSON=1

make feature-report \
  FEATURE_ARTIFACT_ID=<artifact-uuid> \
  FEATURE_REPORT_FAIL_ON_ISSUES=1
```

The report is read-only and contains catalog metadata, input-fitness labels, hashes,
and explicit lineage; it never loads feature values into PostgreSQL. Each artifact is
classified as `complete`, `partial`, `empty`, or `inconsistent`. The per-decision
coverage rows show expected, accepted, unaccounted, and accepted-row counts. An
inconsistent artifact indicates a catalog relationship or count problem and makes
`FEATURE_REPORT_FAIL_ON_ISSUES=1` exit with status 1. A partial artifact can be valid
when the batch manifest explicitly rejected input partitions. The JSON contract is
defined by `schemas/feature-catalog-report.schema.json`.

This is catalog-level coverage, not a feature-value/null-quality report. It does not
infer rejected security IDs, read Parquet values, or compare PostgreSQL registrations
with the feature root; use `make reconcile` for filesystem and hash checks.

### Theme snapshots and the hypothesis loop

The v0.4 research workspace keeps reviewed themes, immutable document artifacts,
derived event proposals, evidence packs, hypothesis revisions, predictions, and
measurement outcomes in PostgreSQL metadata plus content-addressed local artifacts.
Seed the reviewed fixture or use the metadata CLI with explicit JSON payloads:

```sh
make research-seed-theme

make research-theme-snapshot \
  THEME_ID=10000000-0000-4000-8000-000000000001 \
  DECISION_AT=2026-08-29T12:00:00Z

make research-status-report AS_OF=2026-09-30T21:00:00Z
```

The snapshot applies `recorded_at` and validity cutoffs and returns only the latest
reviewed theme revisions, memberships, relationships, indicators, feature
references, and invalidation conditions available at the decision time. The status
report is read-only and summarizes active hypothesis revisions, evidence freshness,
prediction state, upcoming reviews, and measured outcomes. The underlying CLI
operations are `invs-research create-document`, `raw-artifact`, `text-artifact`,
`create-event-proposal`, `event-revision`, `register-pack`, `create-hypothesis`,
`hypothesis-revision`, `hypothesis-evidence`, `create-prediction`,
`freeze-prediction`, `outcome`, and `close-hypothesis`.

Reproduce the complete local acceptance loop, including migration replay, review
authorization, future-reference rejection, frozen-prediction immutability, and
memo export/import:

```sh
make research-acceptance
```

The exact accepted boundary and generated artifact IDs are recorded in the
[v0.4 hypothesis-loop acceptance report](acceptance/2026-08-29-v0.4-hypothesis-loop.md).

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
- A deterministic `market-momentum` artifact with explicit multi-horizon warmup,
  exact decimal/null outputs, and reproducible input lineage.
- A deterministic dataset-level `market-basic` batch over an explicit security list
  and decision schedule, including accepted input fitness labels and explicit rejects.
- A deterministic dataset-level `market-momentum` batch over the same explicit
  security-list and decision-schedule boundary.
- Which validated dataset-level feature batches are cataloged in PostgreSQL, their
  input-fitness labels, decision/universe definitions, and accepted child hashes.
- Which cataloged batch partitions are complete, partial, empty, or inconsistent by
  decision timestamp, including unaccounted partition counts and accepted row counts.
- Current latest-only operational coverage and projection health in Grafana.

### It cannot honestly answer yet

- A historically accurate public-availability timestamp for a current Yahoo or
  FRED download when the source did not provide one.
- A historically accurate public-availability timestamp for CVM IPE; its
  current contract is receipt-time installation replay.
- Historical issuer state from CVM CAD; CAD is current and raw-only.
- Full document contents, extracted filing statements, or a canonical CAD
  history; the current CVM slice is filing metadata plus raw source evidence.
- A complete B3/Brazilian market-data universe; Yahoo Finance `.SA` was verified as a
  candidate price bridge but not as security-master evidence, while official B3
  identity/listing integration, coverage, terms, and historical-fitness acceptance
  remain pending.
- Feature-value null reasons, stale-input attribution, or source-contribution quality
  reporting; a catalog-level coverage/lineage report now exists. Broader market/risk
  feature families beyond `market-momentum`, backtest, strategy signal, portfolio, forecast, ML model,
  execution order, and performance claims also remain out of scope. The catalog
  indexes only validated dataset-level batches, and the feature engine remains a
  deliberately small closed registry.
- A historical identity relationship solely from today's YAML universe mapping.
- A latest fundamental snapshot in PostgreSQL; canonical fundamentals remain in
  Parquet, while PostgreSQL latest-only projections currently cover prices and
  macro observations.

For the current readiness boundary, start with
[`README.md`](../README.md), [`docs/architecture.md`](architecture.md), and the
ADRs, then use this guide as the operator/researcher runbook.
