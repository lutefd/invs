# v1.0 installation, upgrade, and recovery acceptance — 2026-08-30

## Result

The latest disposable installation lifecycle harness rerun passed at commit
`3b1ad115058089fe379f2e5c9cd54eb1413aa92d`:

```sh
INVS_BIND_ADDRESS=127.0.0.1 make v1-install-upgrade-acceptance
```

The machine-readable report was generated at `2026-08-30T14:52:33Z`:

```text
data/research/acceptance/v1/v1-install-upgrade.json
sha256=6d8db933fe8345fb51798e1ef7ec6499170797e51a30f9d97abf38cf3a47a9ef
```

## Scenarios

| Scenario | Evidence | Result |
| --- | --- | --- |
| Fresh install | Isolated Compose project with a new PostgreSQL volume and all forward migrations | Passed |
| Upgrade from pre-v1 schema | Additive backtest and paper schemas removed from the fresh database, then current `make migrate` applied them | Passed |
| Idempotent migration reapply | Current `make migrate` executed a second time against the upgraded volume | Passed |
| PostgreSQL and immutable-data backup/restore | `make backup`, `make backup-validate`, and `make restore` into a clean root and new `restore_*` database | Passed |
| Backup tamper rejection | Existing backup/restore fixture acceptance, including modified-dump rejection | Passed |
| Interrupted-run recovery | Actual daily-cycle CLI failure followed by resume and completion | Passed |

The harness records separate stage logs under
`data/research/acceptance/v1/install-upgrade-20260830T145208Z-1993972/` and removes
the disposable Compose project `invs-v1-install-1993971-1993972`, volume, and restore
database `restore_v1_install_20260830145208_1993972` on exit. The live `invs`
PostgreSQL volume and long-lived services were not used by this acceptance.

## Boundary

This closes the repository-side clean-install, forward-upgrade, backup, restore,
and interrupted-recovery gate. The upgrade rehearsal represents the accepted
pre-v1 boundary by removing only the additive v0.5 backtest and v0.6 paper schemas;
it does not claim a migration from every historical checkout. The daily-cycle
interruption is a deterministic dependency failure/resume drill, not a host
power-loss simulation.

The daily-cycle and durable daily-cycle-report schema revision is `1.1.0`; the
release manifest was refreshed and the lifecycle harness verified the revised
checkout.

The v1.0 release remains pending a genuine wall-clock forward paper record. The
retained v0.6 paper evidence remains recorded/replayed and is not promoted by this
acceptance.
