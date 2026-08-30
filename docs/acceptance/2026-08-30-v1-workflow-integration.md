# v1.0 integrated workflow boundary — 2026-08-30

## Result

The integrated workflow harness passed its structural and runtime checks. It links
the accepted v0.4 thematic research report, the v0.5 US/Brazil backtest reproduction,
and the v0.6 paper reproduction through SHA-256 references. It produces separate
thematic and cross-market reports with selected experiment IDs, selected paper
accounts, checks, data-fitness labels, and limitations.

The reports intentionally finish with `status: attention`, not `passed`. The retained
v0.6 evidence is recorded/replayed installation evidence rather than a genuine
wall-clock forward paper record, which remains a v1 entry criterion. The cross-market
fixture also exposes its bounded copper evidence as `installation_replay_only`.

The genuine path is now fail-closed: `paper-forward-record.schema.json` and the
`invs-forward-record` capture CLI require a recent reconciled paper report and pin
the account, report, and ledger-manifest hashes. Newly generated reports include
an invocation-time UTC `recorded_at`; genuine capture requires that timestamp to be
after the risk check, no more than 24 hours later, and no later than capture. A
self-declared summary without those bound artifacts, a missing/late report
timestamp, or a retained replay fixture cannot satisfy the genuine status check.
This hardening landed in `b5200fa`.

## Coverage

- thematic chain: reviewed theme → evidence pack → hypothesis → prediction →
  selected US equal-weight and momentum comparisons → one paper sleeve;
- cross-market chain: the same dated research references → selected US/Brazil
  experiments → macro revision and Brazil reporting-currency context → two paper
  sleeves → explicitly labeled copper fixture; and
- fail-closed guards: source-report hashes, future-reference rejection, backtest
  artifact/identity reproduction, bias-audit status, account links, paper rebuild,
  backup, reconciliation, and duplicate-cycle checks.

## Reproduction

```sh
make workflow-acceptance
```

The command first reruns `make research-acceptance`, `make backtest-reproduction`,
and `make paper-reproduction`. It then writes:

- `data/research/acceptance/v1/thematic-workflow.json`; and
- `data/research/acceptance/v1/cross-market-workflow.json`.

Those are the default replay outputs. When `V1_FORWARD_RECORD` is supplied, the
harness instead writes to
`data/research/acceptance/v1/genuine/<record_id>/`, derived from the captured
record's canonical ID, so a genuine run cannot overwrite retained replay evidence.
Set `V1_WORKFLOW_OUTPUT_ROOT` to a different safe repository-relative directory
when testing another workflow variant.

The generated reports are ignored runtime evidence and are reproducible from the
committed fixtures and prior acceptance commands. They must not be described as a
v1 release acceptance until the forward-record check changes to `passed`.

The latest replay rerun from `3b1ad115058089fe379f2e5c9cd54eb1413aa92d` kept both
reports at `status: attention` with `forward_record_status: recorded_replay`:

```text
data/research/acceptance/v1/thematic-workflow.json
sha256=c03ed3397f6863769f27eed0214b3032ae2bede5a5c34ab25ee41a881d7d4b6d
data/research/acceptance/v1/cross-market-workflow.json
sha256=ed3ead64c6ce0f5d77bdbc02e9e4d9c341c28878c296f54d20f6c7ca85bdbc32
```

When genuine evidence is available, run the same harness with the captured record
and an aggregate paper report that contains its account IDs:

```sh
make paper-acceptance-report \
  PAPER_ACCOUNT_ID=<account-id> \
  PAPER_DATA_ROOT=/data/research/forward/v1 \
  PAPER_LEDGER_ROOT=/data/research/forward/v1/ledger \
  PAPER_REPORT_OUTPUT=data/research/forward/v1/paper-report.json

V1_FORWARD_RECORD=data/research/forward/v1/forward-record.json \
V1_PAPER_REPORT=data/research/forward/v1/paper-report.json \
V1_PAPER_ACCOUNT_IDS="<account-id>" \
make workflow-acceptance
```

The harness validates the forward record through the normal workflow path. It
expects the thematic report to pass; the cross-market report remains allowed to
be `attention` when its bounded commodity evidence is not backtest-safe.
