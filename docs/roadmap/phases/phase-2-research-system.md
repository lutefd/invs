# Phase 2 — Research System

Canonical plan: [full-version roadmap](../../full-version-roadmap.md)

Execution index: [roadmap index](../README.md)

## Outcome

Phase 2 creates reproducible multi-asset feature datasets and an append-only research
record connecting evidence, themes, hypotheses, frozen predictions, and measured
outcomes.

## Included versions

1. [v0.3 — Research-Grade Feature Platform](../versions/v0.3-feature-platform.md)
2. [v0.4 — Theme Intelligence and Hypothesis Research Loop](../versions/v0.4-hypothesis-loop.md)

## Phase workstreams

- Introduce a controlled, versioned feature-set registry.
- Add deterministic batch publication, resume, catalog, lineage, and coverage
  reporting.
- Version reviewed mappings from vendor facts to comparable research concepts.
- Build one reference AI-infrastructure theme in relational PostgreSQL tables.
- Preserve immutable document text extraction and reviewed LLM-derived events.
- Store hypothesis revisions and frozen predictions without retroactive edits.
- Generate point-in-time evidence packs and independently measured outcomes.

## Phase gate checklist

- [ ] Multi-asset feature batches reproduce in a clean root.
- [ ] Batch interruption resumes without duplicate or conflicting partitions.
- [ ] Feature nulls/rejections are explained through exact lineage.
- [ ] A later filing or macro vintage changes only later eligible feature snapshots.
- [ ] Theme relationships carry evidence, confidence, validity, and revisions.
- [ ] Extracted events retain document hashes and source locators.
- [ ] Frozen predictions cannot be rewritten.
- [ ] One hypothesis is reconstructed exactly at its decision date and later measured.

## Stop conditions

Do not treat model-extracted events as canonical facts, build a generalized graph
platform, or start backtesting on feature sets whose universe, calendar, mappings, or
input manifests are not fingerprinted.
