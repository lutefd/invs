# ADR 0018: Portfolio construction and paper ledger boundary

- Status: Accepted
- Date: 2026-08-29
- Decision owners: repository maintainers

## Context

v0.5 accepts a point-in-time backtest specification and immutable historical
result artifacts. The next boundary is a forward-only paper process that can
turn a frozen strategy signal into a constrained target portfolio, present
orders for approval, simulate fills, and recover the account without losing or
duplicating effects. A paper account must not silently become a broker account
or use a current-state response as its history.

## Decision

1. The paper process is split into four explicit interfaces:

   ```text
   signal context -> target portfolio -> risk decision -> proposed order -> paper fill
   ```

   Strategy code produces targets or weights only. Portfolio construction records
   current holdings, prices, FX/input fingerprints, objective, constraints, and
   fallback behavior. Risk validation is a versioned policy outside strategy code.

2. A paper account specification is immutable and identifies the account, frozen
   strategy, decision/execution clocks, input artifact references, cost policy,
   risk policy, approval policy, and base currency. Every decision freezes the
   exact input fingerprint before a target or order is shown.

3. The first paper broker is an internal deterministic simulator using the v0.5
   after-close/next-session-open semantics. Manual approval is the default. An
   auto-approval mode is allowed only when it is recorded in the account
   specification. No real broker credentials, broker identifiers, or live order
   submission enter the canonical path.

4. Account history is an append-only event ledger. Each event has a deterministic
   idempotency key, sequence, event hash, signed cash/position deltas, and the
   decision/target/order identity that caused it. Current cash, positions, and
   NAV are projections rebuilt from events and checked against valuation events;
   they are never the source of truth.

5. Daily decisions are deterministic from the account specification, input
   artifact hashes, prior ledger, and session date. Repeating a decision, approval,
   fill, or report with the same identity is an idempotent read; conflicting bytes
   fail closed. A later input revision creates a new decision revision and cannot
   mutate an approved target or prior event.

6. The v0.6 acceptance boundary is a bounded recorded/replayed forward paper
   fixture. It must cover twenty or more exchange sessions, a rebalance, a no-op,
   a stale-data halt, a risk rejection, a dividend, a corporate action, restart
   between proposal and fill, exact ledger rebuild, backup/restore, and a
   reconciliation report. It does not claim live-market performance.

## Consequences

- Local JSON artifacts make the full account history portable through the existing
  `data/research` backup path, while PostgreSQL stores an immutable operational
  catalog and event envelope for discovery and monitoring.
- Conservative constraints can reject a target before any order exists. Rejection
  reasons are retained as events and daily reports rather than hidden in logs.
- Portfolio construction is intentionally deterministic and equal-weight/rank
  based in v0.6. Optimizers, shorting, margin, and broker sandboxes remain later
  decisions.
- Paper results remain separate from historical backtest results. Comparisons use
  an explicit promoted backtest reference and never splice the two time series.

## Rejected alternatives

- Treating a broker API's current positions as the canonical account ledger would
  lose rejected, approved, duplicate, and operationally missed decisions.
- Allowing strategy code to bypass risk checks would make an approved target
  impossible to audit independently.
- Reusing mutable backtest results for forward paper events would confuse historical
  knowledge with later observations and break replay identity.
