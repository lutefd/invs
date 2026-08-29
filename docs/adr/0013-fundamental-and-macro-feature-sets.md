# ADR 0013: Fundamental growth and macro-state feature sets

- Status: Accepted
- Date: 2026-08-29

## Context

The v0.3 feature platform has deterministic market artifacts and a closed batch
runner, but a useful research snapshot also needs company fundamentals and a
macro series without collapsing their different identity and availability rules.
SEC facts require reviewed taxonomy and comparability decisions. Macro history
requires a source that preserves revisions and real-time vintages. Neither family
may silently use a current snapshot, fuzzy concept match, or latest-only database
projection.

## Decision

Add two reviewed registry entries at version `1.0.0`:

- `fundamental-growth` is a security-level monthly family with `revenue`,
  `revenue_growth_yoy`, and `operating_margin`. It requires an explicit
  security-to-issuer mapping and the versioned taxonomy registry. The initial
  mappings are exact SEC `Revenue` and `OperatingIncomeLoss` facts in USD, with
  Q4 fiscal-period comparability and reported signs. Its `backtest_safe` label
  follows the exact SEC acceptance/publication timestamp carried by canonical
  fundamentals; missing history, incomparable periods, zero denominators, and
  missing mappings remain explicit null or reject outcomes.
- `macro-state` is a security-attached monthly family with `macro_level` and
  `macro_change_yoy`. It requires explicit source, series, geography, unit, and
  frequency selectors and selects ALFRED historical vintages using both
  `available_at` and `observed_at`, with `vintage_at` as an additional cutoff.
  Revisions therefore change only decisions after their source availability.

Both families use the existing calendar pin and computation-delay contract. Their
derived manifests retain selected canonical manifest/part hashes and the exact
taxonomy mapping hash where applicable. Feature rows remain immutable Parquet;
the batch manifest and quality report carry the operational envelope.

## Consequences

- Fundamental semantics are reviewable and reproducible without pretending that
  every provider concept is interchangeable.
- Macro revision boundaries are visible in feature identity and can be tested at
  the decision clock.
- Explicit mappings and selectors make unsupported or missing inputs visible
  instead of converting them into zeros or current-state values.
- The generic derived-artifact reader can enforce the same timing, decimal, and
  manifest lineage rules for both families.

## Non-goals

This ADR does not add valuation ratios, EPS/FCF taxonomy coverage, issuer-specific
overrides, a macro factor model, a strategy, a backtester, or a historical-fitness
upgrade for receipt-time price data. Those require separate reviewed contracts.
