# v1.0 resilience and historical-bias acceptance — 2026-08-30

## Result

The latest v1 resilience and historical-bias harness rerun passed at commit
`010cfc086c12b384b673cd85908b38d6cc00e0ec`:

```sh
INVS_BIND_ADDRESS=127.0.0.1 make v1-resilience-acceptance
```

The machine-readable report was generated at `2026-08-30T12:27:54Z`:

```text
data/research/acceptance/v1/v1-resilience.json
sha256=cfdf61661e0c06b71c2c5de0cae52ecfa9b8da6a0d11492f0f232a33119f44b4
```

All eleven harness stages passed, including the complete paper-account envelope
preflight and input-byte resume binding enforced by `a4f8868` and `e1ecd4e`.
The temporary restored PostgreSQL database was
`restore_v1_20260830122534_1358159`; the harness removed it on exit and a follow-up
query found no remaining `restore_v1_*` databases. Stage logs remain under
`data/research/acceptance/v1/resilience-20260830T122534Z-1358159/`.

## Scenarios

| Scenario | Evidence | Result |
| --- | --- | --- |
| Clean migration and re-apply | `make historical-truth-db-test` | Passed |
| Historical-bias challenge | `make backtest-reproduction`, including the retained bias audit | Passed |
| Paper interruption, idempotency, and rebuild | `make paper-reproduction` | Passed |
| Daily-cycle resume after a failed dependency | CLI-level `scripts/daily-cycle.sh` failure/resume acceptance via `make v1-daily-cycle-acceptance` | Passed |
| Replay-only forward record is rejected | `make v1-forward-record-acceptance` against the retained v0.6 paper ledger | Passed |
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
