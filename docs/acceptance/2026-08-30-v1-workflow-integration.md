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

The generated reports are ignored runtime evidence and are reproducible from the
committed fixtures and prior acceptance commands. They must not be described as a
v1 release acceptance until the forward-record check changes to `passed`.
