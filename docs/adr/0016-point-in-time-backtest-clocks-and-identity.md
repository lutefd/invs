# ADR 0016: Point-in-time backtest clocks and experiment identity

- Status: Accepted
- Date: 2026-08-29

## Context

The v0.4 research loop can preserve a thesis and a prospective prediction, but it
does not yet simulate decisions, orders, fills, cash, or portfolio state. A v0.5
backtest must be reproducible from the same historical inputs without silently
consulting current PostgreSQL projections, current universe configuration, provider
clients, adjusted prices, or future artifacts.

The first simulator should make timing and accounting boring enough to inspect by
hand. It must also leave a durable identity for the experiment and content-addressed
outputs so an operational retry cannot look like a new research result.

## Decision

### Inputs are explicit, local, and hash-pinned

An experiment specification names every input artifact by kind, stable artifact ID,
relative path, SHA-256, and availability policy. The accepted first implementation
supports local JSON artifacts for:

- raw daily prices with `price_basis=raw`;
- exchange sessions and open/close timestamps;
- historical membership intervals;
- corporate actions, including splits, cash dividends, and delistings;
- FX observations; and
- optional point-in-time feature, macro, or risk-free-series references.

The runner verifies every referenced byte before reading it. It never imports a
provider adapter, calls a network endpoint, reads a latest-only PostgreSQL
projection, or substitutes the current YAML universe. A selected input with
`available_at` after the relevant decision or execution timestamp fails closed.

### Experiment identity is deterministic

The canonical experiment specification contains strategy identity and parameters,
universe and membership fingerprint, calendar and input references, decision and
execution clocks, accounting/cost/risk/metrics policies, date partitions, and optional
random seed. The `experiment_id` is UUIDv5 over the canonical specification without
the identity field itself. Runtime attempt IDs, wall-clock durations, logs, and
container metadata are not identity inputs.

An identical completed specification and input set therefore has one experiment
identity. A changed input path, hash, policy, strategy version, universe fingerprint,
or date partition requires a new identity. A run may be retried operationally, but it
cannot replace an immutable result with different bytes.

### The first execution clock is after-close to next-session open

For each eligible decision session:

1. select information whose availability is no later than the decision session's
   close timestamp;
2. compute the signal and target exposure from prices and membership known at that
   close;
3. create proposed orders without using the same session's close as an execution
   price;
4. execute those orders at the next eligible session's open, skipping holidays and
   non-session dates; and
5. apply fills/costs, then mark holdings at the execution session's close.

The clock policy is named and stored in the specification. The first accepted policy
is `after_close_next_session_open` with one-session execution delay. An explicit
execution-price row is required; missing execution data rejects the affected order or
halts the experiment according to the declared missing-data policy. No forward fill
is permitted.

### Raw prices and actions are separate accounting inputs

The first simulator consumes unadjusted prices and applies actions in event order:

- a split changes quantity by its ratio before the next mark;
- a cash dividend credits the security's currency cash book at its payment session;
- a delisting either liquidates at an explicit settlement price or records a
  rejected/blocked event when no safe settlement is available; and
- membership removal prevents a new target allocation and generates the declared
  exit order at the next eligible execution session.

An adjusted price artifact cannot be used with action rows. This prevents a split or
dividend from being counted both in the price series and in the action ledger.

### Accounting uses exact decimal values and currency books

Cash is held per currency. Positions store exact decimal quantities and local-currency
prices. Target values are computed in the account base currency and converted at the
execution timestamp using an eligible, hash-pinned FX row. NAV is the sum of every
cash book and marked position converted to base currency. An optional reporting
currency is a presentation conversion only and does not alter base-currency
accounting.

Commission, spread, slippage, minimum fee, and tax policies are explicit versioned
values. The first fill price applies half the configured spread plus slippage in the
trade direction; fees are charged in the traded security currency. Missing FX or
invalid conversion rates reject the affected trade rather than inventing a rate.

Metric definitions, annualization, return basis, missing-period behavior, and the
risk-free source are explicit policy fields. A constant annual rate or a point-in-time
risk-free artifact is recorded in the metrics artifact as the exact per-period series
used for excess-return calculations.

### Strategies emit exposures, not orders

The strategy interface receives an immutable point-in-time context and returns target
weights. The first registered baselines are:

- `buy_and_hold` — allocate once at the first executable session and hold;
- `equal_weight` — rebalance active historical members at the declared frequency;
  and
- `momentum_12_1` — rank eligible trailing returns with a declared twelve-month
  lookback and one-month skip, then allocate the top bounded set equally.

Portfolio construction, risk limits, order generation, and fills remain outside the
strategy implementation. A strategy cannot issue broker orders or access future rows.

### Partitions and results are immutable

Every experiment declares non-overlapping development, validation, and holdout date
partitions. Holdout rows are labelled in outputs and cannot be used to fit a
transformation or select parameters. Optional walk-forward windows carry separate
train, calibration, and test intervals with strict chronological ordering; the first
baseline runner records fixed parameters and does not tune against the holdout.

The runner publishes a deterministic result manifest and separate immutable artifacts
for NAV/metrics, holdings, orders, fills, and the accounting/event ledger. Each
artifact records the experiment ID, policy versions, input hashes, and row hashes.
PostgreSQL stores only the small experiment/result registration and input lineage;
the result files remain the authoritative simulation evidence.

## Consequences

- A trade can be traced from a decision session through an order, fill, cash movement,
  action, holding, NAV row, and metric without consulting mutable state.
- Holidays, membership changes, corporate actions, FX availability, and cost policy
  become visible parts of the experiment rather than hidden helper behavior.
- Exact decimal strings make serialized results stable and avoid JSON floating-point
  drift; calculations may use a bounded high-precision decimal context.
- The first simulator is intentionally daily and local. It does not claim intraday
  execution fidelity, broker behavior, or broad source coverage.
- A clean replay must match specification identity, result manifest, row hashes, and
  all exact ledger values. A changed input must create a new experiment identity.

## Rejected alternatives

- Executing at the decision-session close: it leaks a price that is not available at
  the after-close decision boundary and hides the next-session holiday behavior.
- Reading `latest()` PostgreSQL snapshots or current YAML membership: these are
  convenience projections and cannot establish historical universe truth.
- Feeding adjusted prices while also applying actions: this double counts economic
  events and makes attribution impossible.
- Letting strategies return orders: it couples signal logic to execution mechanics
  and prevents common cost, risk, and accounting checks.
- Storing only summary metrics: it loses the order/fill/cash lineage needed to explain
  costs, missing data, and accounting failures.
