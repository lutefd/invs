# Personal Quant Research Platform

A small, self-hosted research stack for collecting point-in-time market data into immutable raw files and normalized Parquet, querying it with DuckDB/Jupyter, and monitoring ingestion through PostgreSQL/Grafana.

The current vertical slice covers Yahoo daily prices, SEC company facts, and FRED macro series. It is research infrastructure, not a trading system, and it does not contain synthetic market observations. Canonical history remains in Parquet for DuckDB/Jupyter research. PostgreSQL has replaceable latest-only price and macro snapshot tables for Grafana; they remain explicitly empty until a successful collection publishes accepted observations.

## Requirements

- Docker Engine with Compose v2
- GNU Make
- outbound HTTPS access for collection

All published ports bind to `127.0.0.1`. No API key is required for the included vertical slice, but SEC requires a descriptive User-Agent with a real contact address.

## First start

```sh
make setup
```

This creates untracked `.env` and `config/config.local.yaml` files with private permissions. Before SEC ingestion:

1. Set `SEC_USER_AGENT` in `.env` to a descriptive value with your contact address.
2. Review the starting universe and date range in `config/config.local.yaml`, then set `providers.sec.enabled: true` when the contact is ready. SEC is disabled in the safe starter configuration.
3. Change `GRAFANA_ADMIN_PASSWORD` if access will be forwarded or shared.

Start the long-lived services and verify them:

```sh
make up
make health
make urls
```

After pulling a version that adds a database migration, upgrade an existing PostgreSQL volume explicitly:

```sh
make migrate
```

Fresh volumes apply both metadata and latest-snapshot migrations automatically. `make migrate` checks for the v2 snapshot schema and applies `000002` only when it is absent, so rerunning it on an upgraded volume is a no-op.

`make urls` prints the current tokenized Jupyter URL. Grafana is at `http://127.0.0.1:3000` by default. If port 3000 is occupied, set `GRAFANA_PORT=3300` in `.env` before startup.

## Collect data

Run every source currently enabled in the local configuration:

```sh
make ingest
```

Or run one source at a time:

```sh
make ingest SOURCE=prices
make ingest SOURCE=sec
make ingest SOURCE=fred
```

The collector is a batch container. Raw payloads land under `data/raw/`; normalized Parquet lands under `data/normalized/`. Each source result is registered in PostgreSQL so Grafana reports actual successful, failed, rejected, and no-change runs.

Collectors are safe to retry with the same logical run key. This command executes one logical source run and immediately retries it with the identical generated key:

```sh
make rerun SOURCE=prices
```

The retry resolves to the already completed ingestion run and does not fetch or write the dataset again. Separate later invocations intentionally receive new run keys and may preserve newly fetched provider revisions with later availability timestamps.

## Research notebook

Open `notebooks/vertical_slice.ipynb` in JupyterLab, or execute it non-interactively:

```sh
make notebook
```

The notebook loads the configured universe to map a price `security_id` to its SEC `issuer_id`; it never pairs independently selected identifiers. Its explicit decision timestamp produces an “as known then” snapshot: the latest price revision for each observed session and the latest eligible SEC/FRED observations whose conservative `available_at` is not later than that decision. Missing pre-ingestion datasets produce typed empty views and an explanatory no-data result.

The DuckDB catalog accepts canonical Parquet schema `1.0.0` only. Its `prices_canonical`, `fundamentals_canonical`, and `macroeconomics_canonical` views preserve exact UTF-8 decimal values, presence flags, and collection provenance. The shorter `prices`, `fundamentals`, and `macroeconomics` research views add `DECIMAL(38,18)` and `DOUBLE` projections for analysis. The exact string columns remain available as `*_value` or `value_text`; use them whenever rounding is unacceptable. Legacy files without `schema_version`, numeric physical decimal columns, unsupported versions, and malformed decimal strings fail closed with actionable schema errors instead of being silently coerced.

Legacy normalized data is handled by an explicit archive/reset policy. The collector validates `data/normalized/` before starting a run and refuses to touch a pre-v1 or otherwise incompatible Parquet file. Move the incompatible normalized tree to a recoverable archive location, keep `data/raw/` intact, and reingest so v1 provenance is obtained from the PostgreSQL source/run catalog. No attempted migration invents missing lineage.

This does not claim a historical backtest. Yahoo and current FRED backfills are only known to this system when collected, so v0 cannot reconstruct what their provider data looked like on past trading dates. A backtest must vary decision timestamps and use sources with defensible historical availability/vintage data.

Optional environment variables can pin a configured slice:

```text
EXAMPLE_SECURITY_ID
EXAMPLE_SEC_CONCEPT
EXAMPLE_FRED_SERIES
EXAMPLE_DECISION_AT
```

## Validation and lifecycle

Run Go tests/vet, JSON Schema validation, Python tests/lint, Compose validation, and the executable notebook:

```sh
make test
make notebook
```

Run all of those checks and build every local image:

```sh
make validate
```

Validate the provisioned market dashboard as strict JSON and ask PostgreSQL to plan every dashboard query against the migrated schema:

```sh
make dashboard-smoke
```

The market dashboard shows configured securities even when their Yahoo snapshot is absent, exposes an explicit no-snapshot row for FRED, and keeps SEC labeled ingestion-only because there is no fundamental snapshot table. Expected FRED series still live only in YAML, so the dashboard deliberately reports FRED source-level presence rather than claiming per-series coverage.

Stop containers while retaining PostgreSQL and Grafana volumes:

```sh
make down
```

Remove containers and those local volumes:

```sh
make clean
```

Runtime data under `data/` is bind-mounted and is not removed by either command.

## Storage truth

- `data/raw/`: preserved provider responses and fetch metadata.
- `data/normalized/`: canonical Parquet queried recursively by DuckDB.
- PostgreSQL: security/source metadata, ingestion-run observability, and replaceable latest-only price/macro projections; never authoritative history.
- Grafana: operational readiness, current snapshot values, coverage gaps, and pipeline health. Missing snapshots stay visible and are never replaced by synthetic observations.

`observed_at`, `published_at`, `available_at`, and `ingested_at` are distinct. The research layer never substitutes a trading-date heuristic when `available_at` is absent; a populated incompatible dataset fails loudly instead.
