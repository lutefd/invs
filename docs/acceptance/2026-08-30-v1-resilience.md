# v1.0 resilience and historical-bias acceptance — 2026-08-30

## Result

The v1 resilience and historical-bias harness passed at commit
`9a0f5587e21f7b3c6048b74a6ae0dcd95c7d62f3`:

```sh
make v1-resilience-acceptance
```

The machine-readable report was generated at `2026-08-30T05:46:30Z`:

```text
data/research/acceptance/v1/v1-resilience.json
sha256=e734d6f962d766c4c6f2ecda2b5dfb546502798d0c048e6a42f5835d64e98832
```

All ten harness stages passed. The temporary restored PostgreSQL database was
`restore_v1_20260830054421_3953892`; the harness removed it on exit and a follow-up
query found no remaining `restore_v1_*` databases.

## Scenarios

| Scenario | Evidence | Result |
| --- | --- | --- |
| Clean migration and re-apply | `make historical-truth-db-test` | Passed |
| Historical-bias challenge | `make backtest-reproduction`, including the retained bias audit | Passed |
| Paper interruption, idempotency, and rebuild | `make paper-reproduction` | Passed |
| Daily-cycle resume after a failed dependency | Focused `test_daily_cycle.py` run in the Jupyter runtime | Passed |
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
