# ADR 0014: Feature quality reports and clean-root replay

- Status: Accepted
- Date: 2026-08-29

## Context

An accepted feature batch can still hide a poor cross-section: some partitions
may be rejected, typed-null outputs may be concentrated in one date, and selected
Parquet parts can contain rows that were not eligible at the decision clock. A
catalog-level PostgreSQL report is intentionally metadata-only, so it cannot
explain feature values or trace them to raw records.

The v0.3 exit gate also needs proof that interruption and relocation do not alter
the immutable result. A happy-path unit test is insufficient evidence for that
boundary.

## Decision

Add a read-only `invs-feature-quality report` path and the
`feature-quality-report.schema.json` contract. The report:

- revalidates the batch and every child artifact;
- verifies every selected canonical input manifest and content-named part hash;
- applies the feature input's `available_at`, `observed_at`, period, and vintage
  cutoffs before counting or explaining rows;
- reports coverage by decision and feature, present/null counts, registry-approved
  null reasons, source contribution, stale input age, explicit rejects, and raw
  record locators; and
- fails closed on missing, unsafe, mismatched, or tampered lineage.

It emits no feature values and performs no PostgreSQL or filesystem mutation. The
existing `make feature-report` catalog path remains separate and continues to
report database registration metadata only.

The acceptance fixture uses 20 deterministic securities, two monthly decisions,
all three v0.3 families, one prepublished child per family, a clean copied data
root, a macro revision boundary, and input/output/registry/taxonomy/universe
tamper probes. It compares the complete interrupted and clean feature trees byte
for byte.

## Consequences

- Operators can distinguish unavailable, rejected, stale, and typed-null results
  while retaining a path to source evidence.
- Quality analysis cannot accidentally use future rows merely because a selected
  Parquet part also contains later observations.
- The clean-root replay test makes path-independent deterministic publication and
  resumable child reuse an explicit release property.
- Reports remain derived diagnostics; immutable manifests and Parquet remain the
  authoritative feature-value boundary.

## Non-goals

This ADR does not add automatic repair, feature-row storage in PostgreSQL,
performance tuning beyond the bounded acceptance fixture, factor performance
analysis, or strategy/backtest behavior.
