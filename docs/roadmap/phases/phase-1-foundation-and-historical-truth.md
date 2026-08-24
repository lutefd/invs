# Phase 1 — Foundation and Historical Truth

Canonical plan: [full-version roadmap](../../full-version-roadmap.md)

Execution index: [roadmap index](../README.md)

## Outcome

Phase 1 turns the current accepted vertical slice into a recoverable daily data
system, then establishes the historical identities, vintages, calendars, corporate
actions, FX, and source policies required for honest simulation.

## Included versions

1. [v0.1 — Foundation Certification and Operations](../versions/v0.1-foundation-certification.md)
2. [v0.2 — Historical Truth and Market Mechanics](../versions/v0.2-historical-truth.md)

The versions were executed sequentially. v0.1 is accepted at `63d479d`; v0.2 is
accepted at `0bfdc27` after its historical identity, vintage, calendar, action, FX,
and US/Brazil bias gates passed.

## Phase workstreams

- Close the provider-wide raw-preservation-on-parse-error gap.
- Prove backup, restore, reconciliation, and unattended daily operation.
- Ingest genuine macro vintages rather than relabeling current FRED backfills.
- Move current YAML identity links toward source-backed historical listings and
  universe membership.
- Define exchange sessions and supported decision clocks.
- Publish corporate actions and reproducible adjustments without overwriting raw
  prices.
- Add versioned FX conversion and canonical SEC filing metadata.
- Admit official B3 identity/listing data and validate the separate Yahoo `.SA`
  price-bridge candidate only after access, policy, fixture, identity, and
  availability review.

## Phase gate checklist

- [x] v0.1 clean-machine collection and manifest verification pass (`63d479d` plus
  the retained prior clean-slice evidence).
- [x] Provider parse failures retain downloaded bytes and publish no false canonical
  rows.
- [x] Backup is restored into a clean root and all durable layers verify.
- [x] Daily collection failures, partials, stale data, and disk pressure are visible.
- [x] One revised ALFRED series selects the correct vintage across exact boundaries
  (`31378be` and the bounded ALFRED acceptance report).
- [x] Historical identifiers and universe membership preserve removed/delisted names
  (`6331d64`, `030b506`, `54c0369`, `19d8115`, and the bounded identity report).
- [x] US and Brazil session/calendar behavior is explicit for the admitted bounded
  XNAS/BVMF artifact versions and decision clocks.
- [x] Split/dividend adjustment and FX calculations reproduce from pinned inputs
  (corporate-action and PTAX acceptance reports).
- [x] Every dataset reachable by the bounded audit has a historical-fitness label.
- [x] Bounded US and Brazil point-in-time bias audits pass (`0bfdc27` and the
  retained v0.2 acceptance artifact).

## Stop conditions

Do not start general feature expansion, strategy APIs, or a backtester if the selected
historical universe still relies on current membership, current identifiers, current
macro vintages, or unversioned price adjustments.
