# v1.0 resilience and historical-bias acceptance — 2026-08-30

## Result

The v1 resilience and historical-bias harness passed at commit
`b471908b3cfe52d82530cc3a8d42df006956b5af`:

```sh
INVS_BIND_ADDRESS=127.0.0.1 make v1-resilience-acceptance
```

The machine-readable report was generated at `2026-08-30T06:51:28Z`:

```text
data/research/acceptance/v1/v1-resilience.json
sha256=9aaa967706714e1a591655a11c07a8c343481de4d28d0c8a7a881418153e31dc
```

All ten harness stages passed. The temporary restored PostgreSQL database was
`restore_v1_20260830064922_39070`; the harness removed it on exit and a follow-up
query found no remaining `restore_v1_*` databases.

## Scenarios

| Scenario | Evidence | Result |
| --- | --- | --- |
| Clean migration and re-apply | `make historical-truth-db-test` | Passed |
| Historical-bias challenge | `make backtest-reproduction`, including the retained bias audit | Passed |
| Paper interruption, idempotency, and rebuild | `make paper-reproduction` | Passed |
| Daily-cycle resume after a failed dependency | CLI-level `scripts/daily-cycle.sh` failure/resume acceptance via `make v1-daily-cycle-acceptance` | Passed |
| Disposable backup/restore integrity | `make backup-restore-acceptance` | Passed |
| Clean-root PostgreSQL restore and reconcile | Live `make backup`, `make backup-validate`, `make restore`, then collector `reconcile --fail-on-issues` | Passed |

The harness also ran `make security-check` with an explicit loopback override,
created and validated a live backup, and checked that the retained v0.5 backtest
and v0.6 paper reports still have passed status, exact reproduction, rebuild, and
reconciliation flags.

## Limitations

- The bias and paper proofs use deterministic retained fixtures; they do not create
  a genuine wall-clock forward paper record.
- The clean-root restore is local Compose evidence, not a multi-host or off-site
  disaster-recovery proof.
- The security step uses a loopback environment override when the operator checkout
  has a deliberate non-loopback `.env` override. The committed Compose default
  remains loopback-only.

This report accepts the v1 operational recovery and bias challenge slice. It does
not by itself make the v1.0 release claim: the thematic and cross-market workflow
reports remain `attention` until a paper account has genuine forward wall-clock
evidence, and the bounded Brazil/commodity coverage limitations remain explicit.
