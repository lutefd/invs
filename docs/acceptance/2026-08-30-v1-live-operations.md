# v1.0 current operational readiness — 2026-08-30

This note records a current host refresh and operational inspection from checkout
commit `92508ffe2432ca9b5ec89e1853d2282338e60544`. It is installation-replay and
operational-readiness evidence, not a v1.0 release acceptance or a live-performance
claim. Operator-owned `.env` and `config/config.local.yaml` values were not copied
into this note.

## Current-source refresh

The enabled sources were refreshed with the supported all-source path:

```sh
INVS_BIND_ADDRESS=127.0.0.1 make ingest SOURCE=all
```

All enabled source runs completed successfully without rejected resources:

| Source | Raw run-manifest SHA-256 | Result |
| --- | --- | --- |
| SEC | `f24a3d8f9bccbbbff206fb925d9650f32434323c439ca8c80b4c57a101ea8c61` | 25,135 fundamentals rows and 1,000 filing rows; 1,000 outputs changed |
| Yahoo | `9ec6a981e03100c22c6a3d215cdd69cda9e5394258bd7663cd88b4a65355bd28` | 1,673 normalized price rows; output unchanged |
| FRED | `115cb756c7f8d3f809e1bfb4c2b1e2ab677e27d22f4a7f4071640eb83f2aec27` | 17,823 observations received; 12 outputs changed |
| ALFRED | `a3951f4b136db15ab3dd540c6afa0b1a6b03c526bf594ddd9a9eda5c627a3d3c` | 389 normalized rows; output unchanged |
| BCB | `b46d3576217a78786bc296e6f61fbc5eb13aca9e7546fd44c74ff1eb555b7687` | 2,416 normalized rows; output unchanged |

These are the sources enabled by the local operator configuration at the time of
the run. Disabled source entries were not represented as fresh or accepted by this
refresh.

## Verification

| Check | Result |
| --- | --- |
| `INVS_BIND_ADDRESS=127.0.0.1 make ops-status` | Passed at `2026-08-30T09:25:14Z`: `operational_status=ok`, enabled source ages 0.13–0.15 hours, projections within threshold, unresolved troubled runs 0, and data disk usage 17% |
| `INVS_BIND_ADDRESS=127.0.0.1 make reconcile` | Passed at `2026-08-30T09:25:15Z`: `issues=0` |
| `INVS_BIND_ADDRESS=127.0.0.1 make notebook` | Passed: vertical-slice notebook executed in the Jupyter container |
| `INVS_BIND_ADDRESS=127.0.0.1 make dashboard-smoke` | Passed: dashboard queries and PostgreSQL `EXPLAIN` checks completed |
| `make backup` followed by `make backup-validate` | Passed for `/home/luis/invs-backups/v1-current-20260830-0917`; 1,550 immutable files and PostgreSQL dump SHA-256 `54a87c4041d4f55d53667f5ce6f5534db1d3bac4748bb5c7e8a62eb5a75246bf` |
| `INVS_BACKUP_ROOT=/home/luis/invs-backups/v1-current-20260830-0917 make ops-status` | Passed with the validated external backup selected for the backup-age check |

The backup is outside the checkout and remains recoverable host state; it is not a
portable repository fixture. The setup target continues to print the standard
reminder to configure a real SEC contact; the operator-owned contact was not
changed or recorded here.

## Release interpretation

The current installation is healthy and its normalized lineage reconciles cleanly.
The Yahoo partition remains receipt-time `installation_replay_only` data, and the
refresh does not create or backdate a genuine wall-clock paper record. The v1.0
release still requires the next real paper session, capture through
`make forward-record-capture`, and the final workflow acceptance against that
record. The bounded Brazil and commodity fitness limitations remain unchanged.
