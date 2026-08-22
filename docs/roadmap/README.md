# Roadmap Execution Index

This directory is the granular execution view of the
[complete full-version roadmap](../full-version-roadmap.md). The complete roadmap
remains the canonical product and architecture plan. These phase and version files
make the same plan easier to execute, review, and hand off in bounded slices.

## How to use this roadmap

1. Read the active phase file to understand the outcome and dependency gates.
2. Work from the active version file and update its progress checklist as cohesive
   units are accepted.
3. Do not start a later version merely because code can be written in parallel. Its
   entry criteria must be true.
4. Keep semantic changes ADR-first, deliver narrow Conventional Commits, and retain
   acceptance evidence against an exact commit and input configuration.
5. When scope changes, update the complete roadmap first, then update the affected
   phase/version execution views in the same documentation slice.

Status meanings:

- **Planned:** no version exit claim has been made.
- **In progress:** at least one scoped work package is active, but exit criteria do
  not yet pass.
- **Accepted:** every version exit criterion and acceptance scenario passes against a
  recorded commit.
- **Blocked:** an explicit external dependency prevents the version gate; missing
  implementation effort alone is not a blocker.

## Phase sequence

| Phase | Versions | Purpose | Phase gate |
| --- | --- | --- | --- |
| [Phase 1 — Foundation and historical truth](phases/phase-1-foundation-and-historical-truth.md) | v0.1, v0.2 | Make collection recoverable, then establish honest historical inputs | US and Brazil point-in-time bias audits pass |
| [Phase 2 — Research system](phases/phase-2-research-system.md) | v0.3, v0.4 | Publish reproducible feature datasets and close the hypothesis loop | One thesis is reconstructable and prospectively measurable |
| [Phase 3 — Simulation](phases/phase-3-simulation.md) | v0.5, v0.6 | Backtest transparently, construct portfolios, and paper trade | Paper ledger reconciles and survives recovery |
| [Phase 4 — Full platform](phases/phase-4-full-platform.md) | v1.0 | Integrate, harden, document, and accept the complete platform | End-to-end and disaster-recovery scenarios pass |
| [Phase 5 — Optional live execution](phases/phase-5-optional-live-execution.md) | Post-v1 | Add one manually approved broker path only after evidence | Separate capital and safety approval |

## Version files

- [v0.1 — Foundation Certification and Operations](versions/v0.1-foundation-certification.md)
- [v0.2 — Historical Truth and Market Mechanics](versions/v0.2-historical-truth.md)
- [v0.3 — Research-Grade Feature Platform](versions/v0.3-feature-platform.md)
- [v0.4 — Theme Intelligence and Hypothesis Research Loop](versions/v0.4-hypothesis-loop.md)
- [v0.5 — Point-in-Time Backtesting and Experiment Tracking](versions/v0.5-backtesting.md)
- [v0.6 — Portfolio Construction and Paper Trading](versions/v0.6-paper-trading.md)
- [v1.0 — Full Personal Research Platform](versions/v1.0-full-platform.md)
- [Post-v1 — Optional Manually Approved Live Execution](versions/post-v1-live-execution.md)

The [cross-version workstreams](cross-version-workstreams.md) apply to every active
version: source admission, data fitness, ADR/schema order, testing, observability,
scale triggers, security, documentation, and universal definition of done.

## Current execution focus

v0.1 is accepted at `63d479d`. The active planned version is now v0.2; its
bounded ALFRED work package is accepted, but the historical-truth version gate is
not. The historical-contract slice in `4d483ac` added ADRs 0006 and 0007, strict
identity/listing/membership/calendar schemas, and executable synthetic US/Brazil
fixtures. The durable publication/resolution boundary and PostgreSQL migration
harness landed in `d963180`; this establishes a tested storage/API boundary but
is not the complete v0.2 exit gate. The bounded B3 public
`InstrumentsConsolidatedFile` slice landed in `c43204f` with exact ticker/ISIN
mapping, raw-first retention, historical identifier/listing publication, and a
live acceptance; it is current/reference evidence only and does not provide
historical lifecycle or universe membership. The next unaccepted cohesive units
are:

1. extend the B3 snapshot path and add a bounded US source with historical
   lifecycle, revision, and universe-membership evidence;
2. admit and publish explicit US and Brazil exchange calendars with source evidence
   and the pinned decision-clock behavior;
3. revisit Yahoo `.SA` only as a price bridge after its source, terms, fixture,
   coverage, and availability checks pass.

The common downloaded-resource result contract and provider failure-preservation
tests landed in `8f2680f`; the separate filing and feature-artifact notebook
inspection landed in `806874a`.
The read-only reconciliation command, backup/restore scripts, and clean-root
recovery drill landed in `0f73e39`; see [the recovery runbook](../operations-recovery.md).

The v0.2 source-selection discovery was tested against live Yahoo `.SA` responses in
[the Yahoo source-admission report](../acceptance/2026-08-13-yahoo-sa-security-master.md).
Yahoo is a candidate price bridge, but it is **not admitted as security-master
evidence**: the checked responses lack stable identity, MIC/primary-listing facts,
historical intervals, membership events, revisions, and historical availability
semantics, and its terms require a separate unattended-access/retention review.
Selective official B3 data now owns the bounded Brazil identity/listing path.
Long-history coverage, lifecycle/membership evidence, and the Brazil bias audit
remain pending. Yahoo remains a separate price-bridge candidate.

No general strategy/backtest implementation should start before the historical-truth
gate in v0.2 passes.
