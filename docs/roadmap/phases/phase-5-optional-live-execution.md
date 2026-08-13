# Phase 5 — Optional Live Execution

Canonical plan: [full-version roadmap](../../full-version-roadmap.md)

Execution index: [roadmap index](../README.md)

## Outcome

This optional post-v1 phase can add one long-only cash-account broker path with
manual approval. It is not required to begin investing with research support in
April 2027.

## Included version

- [Post-v1 — Optional Manually Approved Live Execution](../versions/post-v1-live-execution.md)

## Entry gate checklist

- [ ] Three to six months of stable forward paper evidence exists.
- [ ] A sustained reconciliation-clean period has no unresolved ledger differences.
- [ ] Broker behavior has been compared with paper execution assumptions.
- [ ] Backup/restore, timeout, duplicate-order, partial-fill, and outage drills pass.
- [ ] Maximum capital, order, daily notional, and loss limits are approved.
- [ ] The broker credential has least privilege and can be revoked quickly.
- [ ] A separate operator decision explicitly enables live mode.

## Initial constraints

- One broker, one cash account, long-only whitelisted equities.
- Manual approval for every order batch.
- No margin, leverage, shorting, options, futures, or unattended strategy changes.
- Deterministic client order IDs and append-only local audit events.
- Fail closed on stale data, unknown broker state, reconciliation mismatch, clock
  skew, duplicate intent, or unsupported actions.
- Global kill switch plus per-strategy and per-order/notional limits.

## Stop conditions

Autonomous live trading cannot be enabled as a configuration tweak within this
phase. It would require a later version, substantial live evidence, and a new safety
review.
