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

v0.1 is accepted at `63d479d`, and v0.2 is accepted at `0bfdc27`. The retained
[US/Brazil point-in-time bias audit](../acceptance/2026-08-24-v0.2-point-in-time-bias-audit.md)
passes 13 exact boundary probes and verifies 16 evidence artifacts while keeping
receipt-time prices installation-replay only and unsupported actions blocking. The
v0.3, v0.4, and v0.5 gates are now accepted, and the v0.6 portfolio-construction
and paper-trading gate is accepted at `95a79b5` (feature boundary `f02a57f`); the
next planned version is v1.0.
No strategy or backtester scope was pulled
into the v0.2 exit work. The
historical-contract slice in `4d483ac` added ADRs 0006 and 0007,
strict identity/listing/membership/calendar schemas, and executable synthetic
US/Brazil fixtures. The durable publication/resolution boundary and PostgreSQL migration
harness landed in `d963180`; this establishes a tested storage/API boundary but
is not the complete v0.2 exit gate. The bounded B3 public
`InstrumentsConsolidatedFile` slice landed in `c43204f` with exact ticker/ISIN
mapping, raw-first retention, historical identifier/listing publication, and a
live acceptance; it is current/reference evidence only. Follow-up commits
`6331d64`, `030b506`, `54c0369`, and `19d8115` now publish bounded source-backed
INSM/PETZ3 identity and membership history plus a PETZ3 trading-cessation correction.
A clean live database proved exact correction visibility and delisting boundaries;
see the [identity/listing publication report](../acceptance/2026-08-23-historical-identity-listing-publication.md).
The B3 listed-company corporate-action
evidence adapter landed in `0ad46c8`; it is source-native and live-accepted, but
canonical action publication remains blocked because the endpoint exposes neither
an event ID nor public-availability/correction-revision semantics. See the
[corporate-action evidence report](../acceptance/2026-08-23-b3-corporate-actions-evidence.md).
The official UP2DATA sample now also has a transport-agnostic
`CorporateActionLifeCycleFileV2` parser in `8b9916f` with source event/control
IDs, date fields, action state, and correction fields. It is sample-layout evidence only;
UP2DATA product access remains unaccepted. The later bounded corporate-action
admission publishes its sample revisions only for installation replay and leaves
unknown states unsupported. See the
[UP2DATA sample report](../acceptance/2026-08-23-b3-up2data-corporate-actions-evidence.md).
The B3 2026 market-calendar evidence parser in `6f61a84` now feeds official B3
hours alongside new NYSE calendar/hours parsing (`acd8eec`) and deterministic
canonical publication (`77fee79`). Live acceptance published five bounded BVMF
sessions and all 365 XNYS dates for 2026, then proved pinned after-close
next-session selection across an XNYS holiday. These are current/reference versions
eligible only from receipt time, not historically admitted calendars. See the
[B3 source report](../acceptance/2026-08-23-b3-market-calendar-evidence.md) and
[exchange-calendar publication report](../acceptance/2026-08-23-exchange-calendar-publication.md).
The exact-artifact historical follow-up is accepted in the
[historical calendar and decision-clock report](../acceptance/2026-08-24-historical-calendar-publication.md).
The bounded corporate-action and adjustment follow-up is accepted in the
[corporate-action publication report](../acceptance/2026-08-24-corporate-action-publication.md).
It publishes an exact SEC split, retains B3 sample revisions only from installation
receipt, blocks the unsupported B3 state, and keeps Yahoo split-adjusted prices out
of the raw adjustment path.
The bounded PTAX follow-up is accepted in the
[PTAX FX publication report](../acceptance/2026-08-24-ptax-fx.md). It retains five
official closing bulletins, publishes canonical USD/BRL Parquet, proves exact
before/at availability selection, and reproduces sell-side direct/inverse conversion
from a complete observation pin.
The canonical SEC filing follow-up is accepted in the
[SEC filing publication report](../acceptance/2026-08-24-sec-filing-publication.md).
It publishes 1,001 accession-keyed AAPL filings separately from facts, uses exact
EDGAR acceptance as the knowledge boundary, preserves safe nested primary-document
URLs and source civil dates, and proves exact-key replay plus before/at selection.
The official B3 COTAHIST follow-up is accepted in the
[Brazil price-bridge report](../acceptance/2026-08-24-b3-cotahist-price-bridge.md).
It publishes exact ticker+ISIN-allowlisted, unadjusted PETZ3 prices from a closed
annual archive with receipt-time availability. Yahoo `.SA` is no longer required by
the bounded path and remains unadmitted for unattended raw retention.
v0.3 is **accepted** at `5fc3783`. Its feature-platform acceptance covers the
reviewed taxonomy, three point-in-time feature families, interrupted multi-dataset
replay, ALFRED revision boundaries, strict schemas, and fail-closed lineage
tampering probes; see the [v0.3 acceptance report](../acceptance/2026-08-29-v0.3-feature-platform.md).
Receipt-time prices remain labelled `installation_replay_only`.

v0.4 is **accepted** at `3ac61e1`. The repository now has a reviewed
AI-infrastructure theme, immutable document/text artifacts, source-spanned event
proposals with human review, point-in-time evidence packs and memo round trips, an
append-only hypothesis ledger, frozen predictions, pinned measurement outcomes, and
read-only theme/status reports. The isolated acceptance reconstructs one complete
theme-backed research loop and verifies the required fail-closed mutations; see the
[v0.4 hypothesis-loop acceptance report](../acceptance/2026-08-29-v0.4-hypothesis-loop.md).

Feature-level null reporting, broader taxonomy coverage, and live execution remain
later roadmap boundaries. Rolling calibration execution is also deferred beyond the
accepted fixed-baseline boundary.

v0.5 is **accepted** at `180d5c9`. The bounded daily backtest now has immutable
experiment identity, explicit decision/execution clocks, actions/costs/FX, baseline
strategies, versioned metrics, result comparison, PostgreSQL run lineage,
checkpoint recovery, and a retained five-experiment US/Brazil reproduction with a
13-probe bias audit; see the [v0.5 backtesting acceptance report](../acceptance/2026-08-29-v0.5-backtesting.md).

v0.6 is **accepted** at `95a79b5`. The internal forward-paper engine now constructs
deterministic targets, enforces versioned risk outside strategy code, runs manual or
recorded auto approvals through close-to-next-open simulated fills, and rebuilds
positions/NAV from an append-only ledger. The 22-session recovery drill covers two
strategy sleeves, corporate actions, no-op sessions, stale and risk failures,
duplicate delivery, backup restore, and reconciliation; the PostgreSQL catalog and
`paper-portfolio` dashboard expose operator projections. See the
[v0.6 paper-trading acceptance report](../acceptance/2026-08-29-v0.6-paper-trading.md).

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
Selective official B3 data now owns the bounded Brazil identity/listing path, and
the PETZ3 lifecycle/membership proof plus the combined Brazil audit are accepted.
Broad long-history coverage remains outside the admitted boundary. Yahoo remains a
separate, unadmitted price-bridge candidate.

The v0.6 simulation gate is complete. The next execution focus is v1.0 integration:
connect the accepted collect-to-paper components, harden the operator workflow, and
accumulate a genuine forward record. Broker submission and live execution remain
post-v1 decisions.
