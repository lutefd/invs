# Phase 3 — Simulation

Canonical plan: [full-version roadmap](../../full-version-roadmap.md)

Execution index: [roadmap index](../README.md)

## Outcome

Phase 3 adds a transparent daily-frequency simulator, reproducible experiments,
portfolio construction, versioned risk controls, and a recoverable paper-account
ledger.

## Included versions

1. [v0.5 — Point-in-Time Backtesting and Experiment Tracking](../versions/v0.5-backtesting.md)
2. [v0.6 — Portfolio Construction and Paper Trading](../versions/v0.6-paper-trading.md)

## Phase workstreams

- Pin experiment identity to data, universe, calendar, features, strategy, parameters,
  costs, and environment.
- Implement an inspectable daily event loop with actions, FX, fees, spread, slippage,
  and execution delay.
- Establish boring baseline strategies before sophisticated models.
- Persist balanced orders, fills, cash, holdings, valuations, and attribution.
- Separate signals, target construction, risk validation, proposed orders, and fills.
- Run a forward daily paper cycle and compare it with promoted backtest assumptions.
- Evaluate broker sandboxes and LEAN only as replaceable adapters after local contracts
  are accepted.

## Phase gate checklist

- [ ] Deliberate lookahead and survivorship leaks fail tests.
- [ ] US and Brazil baseline experiments reproduce from immutable inputs.
- [ ] Simulation cash plus positions equals NAV under the declared policy.
- [ ] Costs, actions, FX, missing data, and rejected trades are visible.
- [ ] Holdout attempt counts and walk-forward fitting boundaries are recorded.
- [ ] Portfolio constraints reject unsafe/infeasible targets with explanations.
- [ ] Paper jobs restart without duplicate orders, fills, or cash movements.
- [ ] Paper positions and NAV rebuild exactly from the append-only ledger.
- [ ] Backup/restore and reconciliation pass for paper accounts.

## Stop conditions

Do not add intraday data, leverage, shorting, options, strategy-controlled risk
overrides, or real brokerage submission during this phase.
