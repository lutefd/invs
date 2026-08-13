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

The versions are sequential. ALFRED implementation can be prepared while remaining
v0.1 operational work is finishing, but v0.2 cannot be accepted until v0.1 recovery
and ingestion gates pass.

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
- Admit B3 or a replacement source only after access, policy, fixture, identity, and
  availability review.

## Phase gate checklist

- [ ] v0.1 clean-machine collection and manifest verification pass.
- [ ] Provider parse failures retain downloaded bytes and publish no false canonical
  rows.
- [ ] Backup is restored into a clean root and all durable layers verify.
- [ ] Daily collection failures, partials, stale data, and disk pressure are visible.
- [ ] One revised ALFRED series selects the correct vintage across exact boundaries.
- [ ] Historical identifiers and universe membership preserve removed/delisted names.
- [ ] US and Brazil session/calendar behavior is explicit.
- [ ] Split/dividend adjustment and FX calculations reproduce from pinned inputs.
- [ ] Every dataset reachable by later backtests has a historical-fitness label.
- [ ] Bounded US and Brazil point-in-time bias audits pass.

## Stop conditions

Do not start general feature expansion, strategy APIs, or a backtester if the selected
historical universe still relies on current membership, current identifiers, current
macro vintages, or unversioned price adjustments.
