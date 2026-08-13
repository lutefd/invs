# Post-v1 — Optional Manually Approved Live Execution

> Execution view of the [complete full-version roadmap](../../full-version-roadmap.md).
> Use the [roadmap index](../README.md) for phase order and navigation.

**Status:** Conditional; not part of the April 2027 v1.0 commitment

## Progress tracker

- [ ] Accumulate three to six months of stable forward paper evidence.
- [ ] Maintain a sustained reconciliation-clean period.
- [ ] Approve broker behavior, permissions, capital, and risk limits.
- [ ] Pass sandbox timeout, duplicate, partial-fill, reject, and outage drills.
- [ ] Pass exact dry-run request review without submission.
- [ ] Record a separate explicit operator enablement decision.
- [ ] Start with one cash account, a whitelist, minimal notional, and manual approval.

A checked item records accepted work, not merely code present in a working tree.
Update this tracker only with the implementation/evidence commit or handoff that
supports the state change.
This is a conditional future version, not part of the April 2027 v1.0 commitment.
Beginning to invest in April 2027 can mean using the platform for research and
manually placing trades at a brokerage. Direct integration should wait for evidence,
not the calendar.

## Entry gates

All gates are mandatory:

- at least three to six months of stable forward paper operation for the promoted
  strategy and intended decision frequency;
- no unresolved ledger discrepancies and a sustained reconciliation-clean period;
- documented comparison of paper assumptions with intended broker behavior;
- successful backup/restore and duplicate-order recovery drills;
- explicit maximum capital/notional and loss limits;
- manual review of strategy, risk policy, broker permissions, and legal/tax needs;
- separate operator decision to enable live mode.

## Initial scope

- One cash account, one broker, long-only supported equities, and a bounded whitelist.
- No margin, options, futures, shorting, leverage, or unattended strategy changes.
- Strategy emits targets; portfolio/risk emits proposed orders; only a dedicated
  broker adapter can submit them.
- Every batch requires manual approval after showing evidence, current holdings,
  proposed orders, expected costs, limit/notional checks, and stale-data status.
- Use deterministic client order IDs, idempotent submit/cancel handling, broker status
  polling, local append-only audit events, and post-trade reconciliation.
- Provide global kill switch, per-strategy disable, maximum order/notional/day limits,
  price collars, market-hours checks, and credential revocation instructions.
- Fail closed on unknown broker state, stale inputs, reconciliation mismatch, clock
  skew, duplicate intent, or unsupported action.

## Acceptance before capital

- Exercise the exact broker adapter against sandbox/paper endpoints.
- Inject timeouts before and after submission to prove duplicate orders are avoided.
- Inject partial fills, rejects, cancels, corporate actions, and broker outages.
- Reconcile every remote event to the local ledger and rebuild current state.
- Run a final dry-run mode that generates the exact intended live requests without
  submitting them.
- Start with a separately approved minimal notional and retain manual approval.

Autonomous live trading, if ever desired, requires another version and another safety
review after substantial live evidence. It must not emerge as a configuration change
inside the initial adapter.

---
