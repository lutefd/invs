# Phase 4 — Full Platform

Canonical plan: [full-version roadmap](../../full-version-roadmap.md)

Execution index: [roadmap index](../README.md)

## Outcome

Phase 4 integrates and hardens the collect -> observe -> hypothesize -> test ->
simulate -> measure -> learn loop for regular personal use. It is primarily an
acceptance, operations, compatibility, security, and usability phase.

## Included version

- [v1.0 — Full Personal Research Platform](../versions/v1.0-full-platform.md)

## Phase workstreams

- Pin supported runtime, schema, migration, artifact, strategy, risk, and ledger
  versions.
- Complete the minimum trusted US/global and first-class Brazil coverage needed by
  the accepted research scenarios.
- Orchestrate daily collection, feature, paper, backup, and reconciliation locally.
- Define and observe freshness, recovery, capacity, and reconciliation objectives.
- Ship complete research/operator workflows and limitations without starting a
  frontend project by default.
- Run end-to-end thematic, cross-market, disaster-recovery, and historical-bias
  acceptance scenarios.

## Phase gate checklist

- [x] Clean install, upgrade, backup, restore, and interrupted recovery pass; see the [v1 installation lifecycle acceptance](../../acceptance/2026-08-30-v1-install-upgrade.md).
- [x] Version compatibility manifest rejects unsupported component mixes.
- [x] All data reachable by research/backtest code has a fitness classification.
- [ ] Thematic question reaches a frozen thesis, reproducible backtest, and paper
  decision.
- [x] Cross-market/Brazil scenario exposes missing/current-only data honestly.
- [x] Historical-bias challenge suite passes.
- [x] Paper ledger reconstructs and reconciles.
- [x] Loopback defaults, secret handling, and release scans pass.
- [x] Known limitations identify unsupported claims and instruments precisely.

## Stop conditions

v1.0 does not require autonomous execution, complete worldwide coverage, ML, a
multi-tenant product, intraday data, or a custom frontend.

The data-fitness gate is accepted by the release-pinned matrix in
[`release/data-fitness.json`](../../../release/data-fitness.json), which covers the
five catalog datasets, eight backtest input kinds, four feature families, and the
workflow commodity evidence path. Its validator rejects unlisted sources and keeps
backtests restricted to `backtest_safe` inputs. This is a classification boundary,
not a claim that every listed provider has broad historical coverage.
