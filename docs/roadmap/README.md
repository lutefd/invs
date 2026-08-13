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

The active planned version is v0.1. Feature documentation, the supported
`make feature` / `make feature-validate` command, and its real-data replay have
already landed. The next unaccepted cohesive units are:

1. establish and observe the unattended daily runbook;
2. remaining v0.1 bounded acceptance;
3. post-acceptance ALFRED bias fixtures and the remaining v0.2 historical-truth work.

The common downloaded-resource result contract and provider failure-preservation
tests landed in `8f2680f`; the separate filing and feature-artifact notebook
inspection landed in `806874a`.
The read-only reconciliation command, backup/restore scripts, and clean-root
recovery drill landed in `0f73e39`; see [the recovery runbook](../operations-recovery.md).

The v0.2 Brazil source-selection discovery is to use Yahoo Finance as the primary
B3 bridge through `.SA` ticker mappings for medium- to long-term research, with
selective B3 public datasets for metadata, delistings, corporate actions, and
validation. This is a planning decision only; source admission, fixtures, coverage,
availability semantics, and the Brazil bias audit remain pending.

No general strategy/backtest implementation should start before the historical-truth
gate in v0.2 passes.
