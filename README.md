# Personal Quant Research Platform

A small, self-hosted research stack for collecting point-in-time market data into immutable raw files and normalized Parquet, querying it with DuckDB/Jupyter, and monitoring ingestion through PostgreSQL/Grafana.

Status: this is the first actively developed v1/v0 foundation, not an obsolete product. The post-metadata v0 acceptance passed on 2026-08-12 at commit `9ce22d0` for SEC, Yahoo, FRED, and BCB; the scope limitations below still apply. CVM IPE subsequently passed bounded live acceptance, and the repository now includes the closed deterministic `market-basic` feature engine plus fixture-accepted ALFRED historical-vintage ingestion. A live ALFRED run still requires a configured `FRED_API_KEY`. B3 remains deferred.

The product path is documented in the [full-version roadmap](docs/full-version-roadmap.md), with granular execution views in the [roadmap index](docs/roadmap/README.md).

The current accepted vertical slice covers Yahoo daily prices, SEC company facts, FRED macro series, and BCB SGS macro series. It is research infrastructure, not a trading system, and it does not contain synthetic market observations. Canonical history remains in Parquet for DuckDB/Jupyter research. PostgreSQL has replaceable latest-only price and macro snapshot tables for Grafana; run finalization publishes accepted price/macro candidates to those projections in the same PostgreSQL transaction that closes the run. A partial run may publish successful entities while a parse-error entity publishes no snapshot.

## Requirements

- Docker Engine with Compose v2
- GNU Make
- outbound HTTPS access for collection

SEC requires a descriptive User-Agent with a real contact address. ALFRED requires a 32-character lowercase alphanumeric `FRED_API_KEY`; the key is loaded only from the environment and is excluded from YAML, run inputs, raw metadata, and errors.

## First start

```sh
make setup
```

This creates untracked `.env` and `config/config.local.yaml` files with private permissions. Before SEC ingestion:

The committed `config/config.example.yaml` is the safe starter configuration: Yahoo and FRED are enabled, while SEC, ALFRED, BCB, and CVM are disabled by default. `make setup` copies it to the untracked `config/config.local.yaml`; the completed acceptance used a local override with SEC and BCB enabled and a bounded BCB end date. That local acceptance override is not the committed example configuration.

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

Fresh volumes apply the current forward migration sequence automatically through `000005_nullable_macro_snapshot_value`. For an existing initialized volume, `make migrate` conditionally applies missing snapshot, precision, run-input, and nullable-macro-value changes in order; its schema checks make rerunning it idempotent.

`make urls` prints the current tokenized Jupyter URL. Published PostgreSQL, Jupyter, and Grafana ports bind to `0.0.0.0`; use the server's reachable hostname or IP from another machine. Grafana uses port `3000` by default. If port 3000 is occupied, set `GRAFANA_PORT=3300` in `.env` before startup.

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
# after enabling providers.alfred and setting FRED_API_KEY
make ingest SOURCE=alfred
make ingest SOURCE=bcb
# after enabling providers.cvm and configuring exact universe CVM codes
make ingest SOURCE=cvm
```

The collector is a batch container. Raw payloads land under `data/raw/`; normalized Parquet lands under `data/normalized/`. Each source result is registered in PostgreSQL so Grafana reports actual successful, failed, rejected, and no-change runs.

`make` passes the current full lower-case repository commit into collector builds and runs. For direct Compose use, provide the same value explicitly. A missing value uses the validated `unknown` fallback; invalid or short values are rejected rather than baked into an image:

```sh
export INVS_GIT_COMMIT="$(git rev-parse --verify HEAD | tr '[:upper:]' '[:lower:]')"
docker compose build collector
docker compose --profile collect run --rm collector --source prices
```

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

The notebook loads the configured universe to map a price `security_id` to its SEC `issuer_id`; it never pairs independently selected identifiers. Its explicit decision timestamp produces an “as known then” snapshot using an explicit macro source, so identically named FRED and ALFRED series never compete silently. Missing pre-ingestion datasets produce typed empty views and an explanatory no-data result.

The notebook does not inspect CVM filings yet. The Python catalog now exposes the separate `filings_canonical`, `filings`, and `filings_as_of(...)` interfaces; a future notebook cell must keep CVM filings separate from the price/fundamental snapshot. CVM IPE rows use explicit receipt-time `available_at` and unknown publication precision for live replay, while CAD is a current issuer snapshot excluded from historical filing claims.

The DuckDB catalog accepts canonical Parquet schema `1.0.0` only. Its `prices_canonical`, `fundamentals_canonical`, and `macroeconomics_canonical` views preserve exact UTF-8 decimal values, presence flags, and collection provenance. The shorter `prices`, `fundamentals`, and `macroeconomics` research views add `DECIMAL(38,18)` and `DOUBLE` projections for analysis. The exact string columns remain available as `*_value` or `value_text`; use them whenever rounding is unacceptable. The catalog also exposes canonical `filings_canonical`, a lossless `filings` research view, and `filings_as_of(...)` with explicit historical or installation-replay policy. The Go side and collector now provide the canonical filing-metadata writer and its explicit availability contract. Canonical readers discover only committed `manifest.json` files, validate each manifest and its listed parts, and read only content-named immutable `part-<sha256>.parquet` files. `data.parquet`, unlisted Parquet files, and recursive Parquet glob results are not canonical input. Unmanaged pre-contract or pre-manifest normalized files without the required schema, provenance, or manifest contract fail closed with actionable schema errors instead of being silently coerced. Other incompatible files with unsupported versions, missing numeric physical decimal columns, malformed decimal strings, or invalid manifest/part pairs fail closed as well.

Earlier valid v1 Parquet parts may omit optional `observed_precision`; readers interpret that omission as `unknown`. Those parts remain managed when they are listed by a valid manifest and carry the required schema and provenance. This compatibility case is distinct from unmanaged pre-contract or pre-manifest normalized files, which are rejected.

Unmanaged pre-contract or pre-manifest normalized data is handled by an explicit archive/reset policy. The collector validates `data/normalized/` before starting a run and refuses to touch a pre-contract normalized tree, `data.parquet` or other Parquet files without a manifest, or an invalid manifest/part pair. Do not migrate those files in place: move the complete normalized tree to a recoverable archive location, recreate an empty `data/normalized/`, keep `data/raw/` and the PostgreSQL source/run catalog intact, and reingest. This reset obtains v1 provenance from the new run; no attempted migration invents missing lineage.

This does not yet claim a complete historical backtest. Yahoo prices and current-vintage FRED/BCB backfills remain known only when collected. ALFRED preserves bounded historical macro vintages with a deliberately conservative availability policy, but it does not repair the other datasets' historical gaps and has not yet passed a live credentialed acceptance run.

Optional environment variables can pin a configured slice:

```text
EXAMPLE_SECURITY_ID
EXAMPLE_SEC_CONCEPT
EXAMPLE_MACRO_SOURCE
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

Operational status: the post-metadata v0 acceptance passed on 2026-08-12 at commit `9ce22d0` for SEC, Yahoo, FRED, and BCB. The passing r3 evidence contains raw run manifests and manifest-backed normalized output:

| Source | Retained result evidence |
| --- | --- |
| SEC | 2 raw objects; 25,135 normalized fundamental rows |
| Yahoo | 1 raw object; 1,661 normalized price rows |
| FRED | 2 raw objects; 16,137 `DGS10` rows and 954 `CPIAUCSL` rows |
| BCB | 1 raw object; 2,416 normalized macro rows |

The recoverable evidence archive is retained outside the checkout at `/home/luis/invs-acceptance/2026-08-12-v0-r3` on the acceptance host. This absolute path is an operator reference to the current archive, not a portability or runtime requirement. Earlier partial/failure evidence remains separately retained in sibling archives under `/home/luis/invs-acceptance`; use r3 as the passing acceptance record. If a queued or running run is later confirmed orphaned, only an operator may cancel it with an explicit reason; the collector does not automatically cancel orphan active runs.

Validate the provisioned market dashboard as strict JSON and ask PostgreSQL to plan every dashboard query against the migrated schema:

```sh
make dashboard-smoke
```

The market dashboard shows configured securities even when no accepted Yahoo snapshot exists, exposes explicit no-snapshot rows for macro sources, and keeps SEC labeled ingestion-only because there is no fundamental snapshot table. Expected FRED and BCB series still live only in YAML, so the dashboard deliberately reports source-level presence rather than claiming per-series coverage.

CVM is not included in the original r3 acceptance table above. A later bounded IPE replay passed at implementation commit `742e5ae`: 199 configured Petrobras filing rows were published with complete provenance and identical-key retry behavior. CAD responses remain raw ingestion-only current snapshots, and IPE receipt-time availability supports installation replay rather than historical public-availability claims. See [the current-state handoff](docs/current-state-handoff.md#current-cvm-live-evidence-status) for the retained CVM evidence and [the market-basic acceptance report](docs/acceptance/2026-08-12-market-basic.md) for the first supported feature replay.

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
- `data/normalized/`: manifest-committed canonical Parquet partitions. Readers open `manifest.json`, verify its row counts and hashes, and read only the listed content-named immutable parts; they do not discover `data.parquet` or arbitrary Parquet files by glob.
- PostgreSQL: security/source metadata, ingestion-run observability, and replaceable latest-only price/macro projections; never authoritative history.
- Grafana: operational readiness, current snapshot values, coverage gaps, and pipeline health. Missing snapshots stay visible and are never replaced by synthetic observations.

`observed_at`, `published_at`, `available_at`, and `ingested_at` are distinct. The research layer never substitutes a trading-date heuristic when `available_at` is absent; a populated incompatible dataset fails loudly instead.
