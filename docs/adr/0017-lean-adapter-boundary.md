# ADR 0017: LEAN remains a replaceable simulation adapter

- Status: Accepted
- Date: 2026-08-29

## Context

The v0.5 internal simulator now has explicit point-in-time clocks, immutable
experiment identity, raw-price/action separation, daily orders and fills, exact
currency books, recovery checkpoints, result manifests, and bounded US/Brazil
acceptance evidence. The roadmap calls for a LEAN engineering spike only after
that internal boundary is accepted.

LEAN may improve brokerage and market-mechanics fidelity, but adopting its data
model as the research contract would couple historical truth, feature lineage,
and experiment identity to a replaceable runtime. No pinned LEAN runtime or
brokerage adapter is currently part of this repository, so an equivalence claim
would not be reproducible from the v0.5 checkout.

## Decision

Do not make LEAN a v0.5 runtime dependency. Keep the Python daily simulator as
the canonical execution reference and treat any future LEAN integration as an
adapter with these boundaries:

1. The adapter reads the same hash-pinned experiment specification and local
   artifacts as the reference runner.
2. Canonical clocks, availability timestamps, raw prices, membership, actions,
   FX, cost policy, result identity, and ledger semantics remain repository
   contracts. LEAN internals may not redefine them.
3. The adapter publishes the same result artifact shapes or an explicitly
   versioned translation, preserving the reference result for comparison.
4. A future spike must map one feature/signal artifact and one universe, replay
   one baseline with equivalent timing and costs, compare calendar/action/
   brokerage/result semantics, and report operational coupling before adoption.

The adoption gate is semantic equivalence on the bounded acceptance fixtures plus
a measured fidelity benefit that justifies the added runtime and maintenance
surface. Until that gate passes, the reference simulator remains authoritative.

## Consequences

- v0.5 stays installable without a third-party simulation runtime or network
  service.
- Result provenance and point-in-time guarantees are tested against one small,
  inspectable implementation.
- LEAN can be evaluated later without migrating canonical data or changing
  experiment IDs.
- The v0.5 acceptance report records the deferred adapter boundary; it does not
  claim a LEAN equivalence run that was not executed from a pinned artifact.

## Rejected alternative

Making LEAN the primary backtest contract now would force the repository to
mirror external scheduling, brokerage, corporate-action, and serialization
details before their historical semantics were independently accepted.
