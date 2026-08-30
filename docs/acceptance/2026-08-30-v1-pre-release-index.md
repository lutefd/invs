# v1.0 pre-release evidence index — 2026-08-30

## Release posture

This is the v1.0 pre-release evidence index, not an accepted v1.0 release. The
refreshed resilience evidence was executed against commit
`cf37708cf772d1c4726a50890a3ae24e0bd2b0c3` with
`INVS_BIND_ADDRESS=127.0.0.1` where validation needed to override the operator's
local non-loopback `.env` setting. The tracked documentation commits do not alter
the runtime compatibility contract.

The remaining release gate is a genuine wall-clock forward paper record. Historical
simulation and recorded/replayed paper evidence are not substituted for that
criterion.

## Validation ladder

| Command | Result | Evidence |
| --- | --- | --- |
| `INVS_BIND_ADDRESS=127.0.0.1 make test` | Passed: 196 Python tests, 62 schemas, Go tests/vet, Ruff, release/security checks, backup fixture | Runtime output from 2026-08-30 |
| `INVS_BIND_ADDRESS=127.0.0.1 make release-validate` | Passed: v1.0.0 compatibility contract, 61 schemas, 3 registries, 16 migrations, and complete data-fitness surfaces | Runtime output from 2026-08-30 |
| `INVS_BIND_ADDRESS=127.0.0.1 make notebook` | Passed: empty-safe vertical-slice notebook executed | Runtime output from 2026-08-30 |
| `INVS_BIND_ADDRESS=127.0.0.1 make dashboard-smoke` | Passed: dashboard JSON and PostgreSQL `EXPLAIN` checks | Runtime output from 2026-08-30 |
| `INVS_BIND_ADDRESS=127.0.0.1 make migrate` | Passed: existing PostgreSQL volume remained migration-ready | Runtime output from 2026-08-30 |
| `INVS_BIND_ADDRESS=127.0.0.1 make historical-truth-db-test` | Passed: append-only, rollback, fresh-image, and migration replay checks | Runtime output from 2026-08-30 |
| `INVS_BIND_ADDRESS=127.0.0.1 make reconcile` | Passed: `issues=0` after the normalized Yahoo lineage repair | [Normalized price refresh acceptance note](2026-08-30-normalized-price-refresh.md) |
| `INVS_BIND_ADDRESS=127.0.0.1 make health` | Passed: PostgreSQL accepting connections; long-lived services healthy | Runtime output from 2026-08-30 |
| `INVS_BIND_ADDRESS=127.0.0.1 make workflow-acceptance` | Passed with intentional `attention` status for thematic and cross-market reports | [Workflow integration report](2026-08-30-v1-workflow-integration.md) |
| `INVS_BIND_ADDRESS=127.0.0.1 make v1-daily-cycle-acceptance` | Passed: actual CLI failure/resume path preserved backup and observation evidence | [Resilience acceptance report](2026-08-30-v1-resilience.md) |
| `INVS_BIND_ADDRESS=127.0.0.1 make v1-forward-record-acceptance` | Passed: retained replay-only paper evidence was rejected without writing a record | [Resilience acceptance report](2026-08-30-v1-resilience.md) |
| `INVS_BIND_ADDRESS=127.0.0.1 make v1-resilience-acceptance` | Passed: all eleven stages and seven scenarios | [Resilience acceptance report](2026-08-30-v1-resilience.md) |
| `INVS_BIND_ADDRESS=127.0.0.1 make v1-install-upgrade-acceptance` | Passed: fresh install, pre-v1 upgrade, idempotent reapply, backup/restore, tamper rejection, and interrupted recovery | [Install/upgrade acceptance report](2026-08-30-v1-install-upgrade.md) |

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
- `cf37708` — repeatable rejection of replay-only forward-record capture.
- `c011ab6` — disposable fresh-install, upgrade, backup/restore, and interrupted-recovery acceptance.

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
| Data-fitness matrix | 29 entries cover 5 catalog datasets, 8 backtest kinds, 4 feature families, and workflow evidence; unlisted sources reject | Accepted repository classification boundary; source breadth remains bounded |
| Normalized Yahoo refresh | Legacy `raw` partition and stale derived artifact archived; preserved raw evidence reingested as `split_adjusted`; reconciliation clean | Installation-replay preparation only; not forward evidence |
| Wall-clock forward paper record | Not present; retained v0.6 evidence remains replay-only | Required for v1.0 release acceptance |

Generated JSON reports under `data/research/acceptance/` are ignored runtime
evidence. Regenerate them with the commands above; do not treat a retained replay
report as a live-performance claim.
