# ALFRED CPIAUCSL bounded live acceptance

## Result

The bounded ALFRED historical-vintage slice passed live acceptance on 2026-08-13
UTC against repository commit `31378be6074548bdc01e2908406bd11ddd12bcb8`.
Credentials were supplied only through the Compose environment. No key value is
stored in this report, YAML, run inputs, raw metadata, manifests, or recorded errors.

Accepted request boundary:

- source: `alfred`
- series: `CPIAUCSL`
- real-time range: `1776-07-04` through `2026-08-11`
- observation range: `2019-01-01` through `2026-07-01`
- output type: `1` (observations by real-time period)
- format: JSON

## Primary run

- ingestion run ID: `2e659fda-c803-45b0-af1f-a49ff7349d90`
- logical run key: `alfred-cpi-live-20260811/alfred`
- status: `succeeded`
- records received/written/rejected: `389 / 389 / 0`
- raw objects: `1` (`38,392` bytes)
- raw object SHA-256: `04ccad5eb0a7ca137aa0b3af70b42b07ab52cad7e24c28351d154344f63a0db4`
- raw manifest SHA-256: `4b0ff6d258f41d9cb91bd5f24a9e87b83ba85ab2dffeb99cb2c10770ed559533`

The exact run-key retry returned the existing terminal run and performed no fetch or
write.

## Independent replay

A second logical run, `alfred-cpi-live-20260811-replay/alfred`, fetched the same
bounded provider response and completed successfully:

- ingestion run ID: `c4162efd-9b77-4020-98b5-fc43d419f532`
- records received/written/rejected: `389 / 0 / 0`
- raw objects: `1`
- raw manifest SHA-256: `e7efd2bd5458afdd31156ccfa23c4ccba5ff0cec9bf9b4676e8725cbca22031f`

The zero-row canonical replay verifies that historical revision identities did not
drift across a fresh provider fetch.

## Canonical and operational checks

Manifest-backed DuckDB readback reported:

- `389` ALFRED rows across `90` observation dates;
- observation range `2019-01-01` through `2026-06-01` in the returned source data;
- maximum revision ordinal `5`;
- one explicit null vintage;
- `CPIAUCSL` remained source-separated: `389` ALFRED rows and `954` current FRED rows.

PostgreSQL reported a separate accepted ALFRED latest projection for `CPIAUCSL` and
a nullable macro snapshot value column. The persisted run metadata contained no
`api_key` field. Raw manifest attributes contained only the bounded non-secret
request contract, pagination values, and semantic dimensions; the stored object hash
matched the adapter/manifest hash.

## Validation performed

- `make test`: all Go tests/vet, 13 JSON Schemas, 50 Python tests, and Ruff passed.
- `make notebook`: the vertical-slice notebook executed successfully.
- `make dashboard-smoke`: every provisioned dashboard query explained successfully
  against PostgreSQL.
- Forward migration `000005_nullable_macro_snapshot_value` applied successfully.
- PostgreSQL, Jupyter, and Grafana images were rebuilt; all long-lived services were
  healthy after recreation.

This accepts the bounded ALFRED ingestion work package. It does not accept all of
v0.2 or establish historical correctness for Yahoo, current FRED/BCB, corporate
actions, universe membership, calendars, FX, or filings.
