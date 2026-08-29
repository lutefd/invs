# ADR 0012: Market momentum and risk feature set

- Status: Accepted
- Date: 2026-08-29

## Context

The v0.3 platform has a reviewed `market-basic` feature set and a dataset-level
batch/catalog path, but it does not yet provide a reusable market/risk family. The
next family must remain small enough to review against the existing daily price
contract while making its lookbacks, warmup behavior, and risk formulas explicit.

The available daily price bridge is receipt-time evidence. It can support
installation replay and research inspection, but it cannot be relabelled as a
historically safe public-availability source by the feature producer.

## Decision

Add the controlled `market-momentum` feature set at version `1.0.0`. It is a
security-level, daily feature set over one deterministic daily price series. The
registry requires the same exchange calendar pin and
`after_close_next_session` decision clock as `market-basic`.

### Exact outputs

All outputs are canonical decimal strings or typed nulls:

- `return_1m`: `close_t / close_(t-21) - 1`;
- `return_3m`: `close_t / close_(t-63) - 1`;
- `return_6m`: `close_t / close_(t-126) - 1`;
- `return_12m`: `close_t / close_(t-252) - 1`;
- `realized_volatility_1m`: the annualized sample standard deviation of the
  trailing 21 one-observation simple close returns, multiplied by `sqrt(252)`;
  and
- `max_drawdown_1m`: the minimum running `close / prior_peak - 1` across the
  trailing 21 eligible closes, including the current close.

The return horizons use eligible observations rather than guessed calendar days.
The longest output requires 253 closes; the family declares that as its maximum
lookback and uses `null_until_available`. Shorter outputs become non-null as their
own prerequisites become available. A missing close in a required window produces a
typed null; a zero denominator or invalid price series fails closed as a rejected
partition.

### Point-in-time and fitness boundary

The producer selects prices only through `ResearchCatalog.point_in_time_inputs`,
requiring both `available_at <= decision_at` and `observed_at <= decision_at`. The
selected manifest and part hashes, calendar pin, feature-set identity, and decision
timestamp participate in the input fingerprint. The derived artifact availability
remains the maximum selected input availability plus the declared computation delay.

The registry labels the price input `installation_replay_only` with
`conservative_receipt_time` availability. This feature set therefore does not make a
backtest-safety claim, add a benchmark, infer liquidity, or use a latest-only
projection.

## Consequences

- Market momentum and two bounded trailing risk diagnostics have a stable, reviewable
  identity and can use the existing artifact, batch, and catalog contracts.
- Decimal arithmetic and a fixed high-precision calculation context keep repeated
  publications deterministic without emitting JSON floating-point values.
- Long lookbacks remain visibly unavailable during warmup instead of being shortened
  or forward-filled.
- The family is intentionally limited to one daily price series; benchmark-relative
  strength, turnover, downside-volatility policy, fundamental features, and macro
  features require separate reviewed contracts.

## Non-goals

This ADR does not add a strategy, signal, ranking, portfolio, backtest, execution,
benchmark feed, liquidity model, or historical-fitness upgrade for receipt-time
prices. It does not add feature-level null-reason storage beyond the typed-null
contract.
