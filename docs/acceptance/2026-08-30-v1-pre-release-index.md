# v1.0 pre-release evidence index — 2026-08-30

## Release posture

This is the v1.0 pre-release evidence index, not an accepted v1.0 release. The
current operational snapshot below was captured from checkout commit
`92508ffe2432ca9b5ec89e1853d2282338e60544`. The current validation and v1
acceptance reruns were executed from commit `e1ecd4e32480a5a53ceace87d195200e80ca2f9a` with
`INVS_BIND_ADDRESS=127.0.0.1` where validation needed to override the operator's
local non-loopback `.env` setting. The tracked documentation commits do not alter
the runtime compatibility contract; the later paper price-basis compatibility
changes are recorded below.

The remaining release gate is a genuine wall-clock forward paper record. Historical
simulation and recorded/replayed paper evidence are not substituted for that
criterion.

## Validation ladder

| Command | Result | Evidence |
| --- | --- | --- |
| `INVS_BIND_ADDRESS=127.0.0.1 make test` | Passed: 208 Python tests, 63 schemas, Go tests/vet, Ruff, release/security checks, backup fixture | Runtime output from 2026-08-30 |
| `INVS_BIND_ADDRESS=127.0.0.1 make release-validate` | Passed: v1.0.0 compatibility contract, 61 schemas, 3 registries, 16 migrations, and complete data-fitness surfaces | Runtime output from 2026-08-30 |
| `INVS_BIND_ADDRESS=127.0.0.1 make ingest SOURCE=all` | Passed: all five enabled source runs completed without rejected resources | [Current operational readiness note](2026-08-30-v1-live-operations.md) |
| `INVS_BIND_ADDRESS=127.0.0.1 make ops-status` | Passed: current enabled sources and projections within threshold; disk usage 17% | [Current operational readiness note](2026-08-30-v1-live-operations.md) |
| `INVS_BIND_ADDRESS=127.0.0.1 make notebook` | Passed: empty-safe vertical-slice notebook executed | Runtime output from 2026-08-30 |
| `INVS_BIND_ADDRESS=127.0.0.1 make dashboard-smoke` | Passed: dashboard JSON and PostgreSQL `EXPLAIN` checks | Runtime output from 2026-08-30 |
| `INVS_BIND_ADDRESS=127.0.0.1 make migrate` | Passed: existing PostgreSQL volume remained migration-ready | Runtime output from 2026-08-30 |
| `INVS_BIND_ADDRESS=127.0.0.1 make historical-truth-db-test` | Passed: append-only, rollback, fresh-image, and migration replay checks | Runtime output from 2026-08-30 |
| `INVS_BIND_ADDRESS=127.0.0.1 make reconcile` | Passed: `issues=0` after the normalized Yahoo lineage repair | [Normalized price refresh acceptance note](2026-08-30-normalized-price-refresh.md) |
| `INVS_BIND_ADDRESS=127.0.0.1 make health` | Passed: PostgreSQL accepting connections; long-lived services healthy | Runtime output from 2026-08-30 |
| `make backup` plus `make backup-validate` | Passed: current external backup validated with 1,550 immutable files | [Current operational readiness note](2026-08-30-v1-live-operations.md) |
| `INVS_BIND_ADDRESS=127.0.0.1 make workflow-acceptance` | Passed with intentional `attention` status for thematic and cross-market reports | [Workflow integration report](2026-08-30-v1-workflow-integration.md) |
| `INVS_BIND_ADDRESS=127.0.0.1 make v1-daily-cycle-acceptance` | Passed: actual CLI failure/resume path preserved backup and observation evidence | [Resilience acceptance report](2026-08-30-v1-resilience.md) |
| `INVS_BIND_ADDRESS=127.0.0.1 make v1-forward-record-acceptance` | Passed: retained replay-only paper evidence was rejected without writing a record | [Resilience acceptance report](2026-08-30-v1-resilience.md) |
| `INVS_BIND_ADDRESS=127.0.0.1 make v1-resilience-acceptance` | Passed: all eleven stages and seven scenarios | [Resilience acceptance report](2026-08-30-v1-resilience.md) |
| `INVS_BIND_ADDRESS=127.0.0.1 make v1-install-upgrade-acceptance` | Passed: fresh install, pre-v1 upgrade, idempotent reapply, backup/restore, tamper rejection, and interrupted recovery | [Install/upgrade acceptance report](2026-08-30-v1-install-upgrade.md) |
| `INVS_BIND_ADDRESS=127.0.0.1 make paper-acceptance-report PAPER_ACCOUNT_ID=... PAPER_DATA_ROOT=/data/research/acceptance/v0.6/reproduction PAPER_LEDGER_ROOT=/data/research/acceptance/v0.6/reproduction/ledger` | Passed: derived all five v1 paper checks from the retained deterministic ledger without source mutation | Runtime output from 2026-08-30 |

## v1 implementation checkpoints

- `f8abe40` — fail-closed runtime and contract compatibility manifest;
- `31e34b8` — specification-driven, resumable local daily-cycle runner;
- `d5b9c22` — content-addressed thematic and US/Brazil cross-market workflow reports;
- `586d79e` — loopback/security checks, backup/restore hardening, health objectives,
  and incident/capacity runbooks; and
- `9a0f558` — resilience and historical-bias acceptance harness; and
- `3f96623` — CLI-level daily-cycle failure/resume acceptance through the actual
  shell entrypoint;
- `e272e3d` — hash-pinned forward paper-record contract and capture CLI; and
- `b471908` — integration correction keeping forward validation isolated from
  feature-module imports; and
- `be16303` — full paper-account/report validation and append-only ledger replay
  for genuine forward evidence; and
- `cf37708` — repeatable rejection of replay-only forward-record capture;
- `c011ab6` — disposable fresh-install, upgrade, backup/restore, and interrupted-recovery acceptance;
- `97e9029` — isolated replay and genuine workflow evidence output directories;
- `a1ba6c7` — paper-only admission of explicitly labeled split-adjusted prices;
- `22e71cf` — shared input-schema and release-manifest alignment for that boundary; and
- `7153626` — mixed price-basis rejection regression coverage;
- `b450f9f` — read-only aggregate paper acceptance-report derivation from an immutable ledger; and
- `c35ee45` — release compatibility hash refresh for the paper implementation; and
- `a4f8868` — complete paper-account envelope validation before daily-cycle execution; and
- `e1ecd4e` — bind daily-cycle resume identity to referenced input bytes.

The version compatibility and forward-upgrade procedure is in the
[release guide](../release-compatibility.md). The operator path is in the
[recovery runbook](../operations-recovery.md).

## Accepted versus pending

| Area | Current evidence | Release interpretation |
| --- | --- | --- |
| v0.1–v0.6 foundations | Accepted bounded reports and reproductions | Complete for their documented scopes |
| Runtime and compatibility | Manifest validation passes; unsupported mixes fail closed | Accepted implementation boundary |
| Security and recovery | Loopback defaults, secret scan, backup/restore, restore/reconcile drill pass | Accepted operational boundary |
| Installation lifecycle | Fresh install, additive pre-v1 upgrade, idempotent migration reapply, isolated backup/restore, and daily-cycle recovery pass | Accepted repository-side lifecycle boundary |
| Historical bias | 13-probe retained bias suite passes | Accepted challenge boundary |
| Thematic/cross-market workflow | Both reports validate and link all layers | `attention` until forward evidence exists |
| Brazil/commodity coverage | Explicit bounded fitness labels and missing coverage | Not a broad coverage claim |
| Forward-record capture path | Contract, hash binding, stale/reconciliation checks, and CLI acceptance tests pass | Ready to capture only after a real recent paper session |
| Paper price-basis boundary | Split-adjusted paper inputs are admitted without actions; backtests remain raw-only; mixed bases reject | Accepted implementation boundary; requires a genuine session for release |
| Data-fitness matrix | 29 entries cover 5 catalog datasets, 8 backtest kinds, 4 feature families, and workflow evidence; unlisted sources reject | Accepted repository classification boundary; source breadth remains bounded |
| Normalized Yahoo refresh | Legacy `raw` partition and stale derived artifact archived; preserved raw evidence reingested as `split_adjusted`; reconciliation clean | Installation-replay preparation only; not forward evidence |
| Wall-clock forward paper record | Not present; retained v0.6 evidence remains replay-only | Required for v1.0 release acceptance |
| Aggregate paper acceptance report | Derived checks pass for the retained deterministic ledger; source account remains replay-only | Ready to regenerate for a genuine account, but not itself a wall-clock record |

Generated JSON reports under `data/research/acceptance/` are ignored runtime
evidence. Regenerate them with the commands above; do not treat a retained replay
report as a live-performance claim.
