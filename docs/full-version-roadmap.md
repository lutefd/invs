# Personal Quant Research Platform — Full-Version Roadmap

## Status and purpose

This document scopes the work from the current foundation to a complete personal
research and paper-trading platform. It is an execution plan, not a claim that the
listed versions already exist and not a commitment to ship every possible data
source.

For day-to-day execution, use the separate
[phase and version roadmap index](roadmap/README.md). This file remains the complete
canonical plan; the split files provide granular progress checklists and handoff
boundaries.

Planning snapshot:

- Planning date: 2026-08-12.
- Repository: `/home/luis/dev/invs`.
- Committed baseline used for the broad version scoping: `9168105`
  (`docs: record post-handoff continuation`). Later ALFRED work is tracked through
  the v0.2 execution file and is not called accepted until its version gates pass.
- The implementation boundary described by
  [current-state-handoff.md](current-state-handoff.md) is `742e5ae`
  (`feat(features): publish deterministic market artifacts`).
- The post-handoff continuation also includes the supported feature operator command
  at `e581c5d`, aligned feature documentation at `37e8812`, and the retained
  `market-basic` operator acceptance report at `b7dcac3`.
- `v0.1` through `v1.0` below are proposed planning labels. They are not existing Git
  tags or promises of backward compatibility.
- The target date for a useful full version is April 2027. The dates in this document
  are sequencing windows, not fixed deadlines.

The definition of a **full version** in this roadmap is deliberately narrower than
the complete long-term vision. `v1.0` should let one person collect and audit data,
research a point-in-time hypothesis, run a realistic historical test, construct a
constrained portfolio, and follow it in paper trading. It does not need autonomous
real-money execution, intraday data, a distributed platform, or a complex web UI.

## Current baseline

The next phase starts from a substantial, accepted data foundation rather than an
empty repository. The following should be treated as existing contracts to extend,
not systems to replace:

- Go collectors and provider adapters for Yahoo daily prices, SEC company facts,
  FRED, ALFRED historical vintages, BCB SGS, and staged CVM data.
- Immutable raw objects, per-run raw manifests, SHA-256 verification, and raw-first
  collector ordering.
- PostgreSQL identities, versioned security identifiers, source configuration,
  ingestion-run state, effective run inputs, and latest-only price/macro projections.
- Canonical schema `1.0.0`, exact decimal strings, explicit timestamp precision,
  provenance on every published row, content-named Parquet parts, and atomic
  `manifest.json` publication.
- DuckDB/Jupyter research access that follows manifests and exposes explicit
  point-in-time selection.
- Separate filing views for CVM IPE, with receipt-time availability and no historical
  publication-time claim.
- A closed deterministic `market-basic` feature artifact with input fingerprints,
  immutable output parts, and tamper detection.
- Supported `make feature` and `make feature-validate` operator paths, accepted
  against the real manifest-backed AAPL slice with identical replay and an exact
  point-in-time cutoff check.
- Grafana pipeline-health and latest-snapshot dashboards.
- A validation surface built around Go tests and vet, schema validation, Python tests
  and Ruff, notebook execution, dashboard SQL smoke tests, image builds, migration
  checks, and live acceptance archives.

The following baseline limitations drive the version order:

- Current Yahoo, FRED, and BCB backfills do not establish historical knowledge
  availability.
- ALFRED vintage ingestion and exact-boundary fixture selection are implemented but
  not live-accepted; corporate actions, historical universe membership, exchange
  calendars, and historical identifier resolution remain absent.
- B3 market data and broad Brazilian instrument discovery are not yet admitted.
- SEC filing metadata is not yet a canonical dataset, and CVM CAD is intentionally
  raw-only.
- The feature engine publishes a single bounded feature set and lacks a dataset-wide
  runner and catalog.
- There is no theme graph, document-event pipeline, hypothesis ledger, backtester,
  portfolio engine, paper account, or live execution.

## Product boundary and non-negotiable rules

Every version must preserve these rules.

### Truth layers remain separate

```text
vendor evidence
    -> canonical observations known at explicit times
        -> deterministic feature artifacts
            -> signals and hypotheses
                -> backtest or paper decisions
                    -> positions and measured outcomes
```

Raw source evidence, canonical facts, derived features, research judgments, and
portfolio actions must remain distinct and traceable. A later layer may reference an
earlier one; it must not silently rewrite it.

### Historical claims require evidence

A row collected today is not automatically information that was available at its
historical observation date. A source enters point-in-time backtests only after its
publication/vintage policy is documented and tested. Otherwise it remains useful for
current research or installation-time replay only.

### Existing storage boundaries remain authoritative

- Raw files are immutable evidence.
- Manifest-backed Parquet is authoritative analytical history.
- PostgreSQL owns identities, operational state, catalogs, and small transactional
  research records. It does not become a duplicate bulk time-series lake.
- Grafana and any future API expose projections; they do not define research truth.
- Feature, signal, experiment, and portfolio artifacts follow the same immutable,
  content-addressed publication approach unless an ADR proves a different boundary is
  necessary.

### Complexity must be earned

Stay with Docker Compose, filesystem storage, PostgreSQL, Go, Python, Parquet,
DuckDB, Jupyter, and Grafana while they meet the workload. A scheduler, object store,
LEAN integration, or broker adapter is admitted only by a measured use case. Kafka,
Kubernetes, Spark, tick storage, and a custom distributed database are outside this
roadmap.

### Safety outranks automation

The April 2027 outcome may inform manually placed investments and run paper
portfolios. It does not require the platform to place real orders. Any later live
path must be disabled by default, manually approved, bounded, auditable, and able to
fail closed.

## Repository implementation conventions

All phases should follow the patterns already established in the repository:

1. Write or update an ADR before introducing a new semantic boundary such as
   corporate-action adjustment, feature-set versioning, a backtest clock, portfolio
   accounting, or broker execution.
2. Add strict JSON Schemas and fixtures for new durable interchange contracts.
3. Keep provider HTTP and parsing behavior in `internal/providers/<source>` and
   vendor-neutral models in `internal/model`.
4. Store provider bytes before parsing and return bytes plus parse errors through a
   uniform adapter result contract.
5. Publish canonical partitions through the existing content-named part and atomic
   manifest protocol. Readers enumerate manifests; they never recursively glob data.
6. Put deterministic research calculations in `python/research`, with tests in
   `python/tests`. Keep collection and network access out of the research layer.
7. Use PostgreSQL migrations for transactional metadata and operational state. Every
   forward migration needs a rollback or an explicit irreversible-migration ADR and a
   restore procedure.
8. Extend the Makefile with small operator-facing commands rather than requiring
   undocumented shell sequences.
9. Deliver work as narrow Conventional Commits. A schema/ADR slice, implementation
   slice, projection/UI slice, and live acceptance slice should usually remain
   separately reviewable.
10. Preserve missing state. Dashboards and notebooks must show gaps; they must not
    manufacture example market observations.

## Version dependency map

```text
current foundation
      |
      v
v0.1 certify and operate the existing foundation
      |
      v
v0.2 establish historical truth and market mechanics
      |
      v
v0.3 publish research-grade feature datasets
      |
      v
v0.4 close the evidence -> hypothesis research loop
      |
      v
v0.5 run reproducible point-in-time backtests
      |
      v
v0.6 construct portfolios and forward paper trade
      |
      v
v1.0 harden and accept the complete personal platform
      |
      v
post-v1 optional manually approved live execution
```

The order is intentional. Source expansion may occur inside the relevant data
version, but strategy work must not bypass the v0.2 historical-truth gate. Paper
trading may begin with a simple baseline as soon as v0.5 is accepted; it does not
wait for machine learning or document intelligence.

## Roadmap summary

| Version | Indicative window | Primary outcome | Hard exit gate |
| --- | --- | --- | --- |
| v0.1 | Aug–Sep 2026 | Certified, recoverable foundation | Fresh-machine acceptance and restore/replay pass |
| v0.2 | Sep–Nov 2026 | Historically honest datasets | Bias audit passes for one US and one Brazil research slice |
| v0.3 | Nov–Dec 2026 | Batch, versioned feature platform | Reproducible multi-asset feature publication pass |
| v0.4 | Dec 2026–Jan 2027 | Evidence, themes, and hypotheses in one loop | One thesis is stored, reproduced, and prospectively tracked |
| v0.5 | Jan–Feb 2027 | Realistic point-in-time backtesting | Baseline strategies reproduce with costs and bias controls |
| v0.6 | Feb–Mar 2027 | Portfolio construction and paper trading | Daily paper cycle reconciles and survives recovery drills |
| v1.0 | Mar–Apr 2027 | Full personal research platform | End-to-end user scenarios and operational acceptance pass |
| Post-v1 | After sustained paper evidence | Optional manual live-order path | Separate safety review and explicit operator enablement |

Capacity should favor the critical path over strict calendar boundaries. Data should
continue collecting throughout all phases so that the forward paper period grows even
while later components are built.

---

# v0.1 — Foundation Certification and Operations

## Goal

Turn the current implementation into a boring, repeatable base that can be trusted
for months of unattended data collection and later research. This version closes
known foundation gaps; it does not broaden the product into strategy logic.

## User outcome

From a fresh clone, the user can configure a bounded universe, start the stack,
collect each accepted source, query canonical manifests, publish and inspect the
first feature artifact, see pipeline health, back up the durable state, and recover
or replay it using documented commands.

## Entry criteria

- Current Go, schema, Python, notebook, dashboard, and container checks pass.
- The post-metadata SEC/Yahoo/FRED/BCB acceptance and current CVM IPE acceptance
  evidence remain available for comparison.
- Any working-tree changes that predate this roadmap are reviewed separately and are
  not overwritten by roadmap work.

## Scope

### 1. Reconcile contracts and documentation — completed before roadmap finalization

- Update README, architecture, ADR 0005, schema documentation, and usage instructions
  to agree that `market-basic` calculation and publication now exist.
- Preserve the closed four-feature registry and explicitly retain strategy,
  backtesting, labels, and ML as non-goals.
- Add a short release/acceptance index under `docs/` that maps an accepted boundary to
  Git commit, schema versions, migration versions, commands, and external evidence
  archive. Do not copy large raw datasets into Git.
- Document the difference between repository release state, local collected data,
  external acceptance archives, and replaceable PostgreSQL projections.

The feature/CVM wording alignment landed in `37e8812`. The remaining v0.1
documentation work is the release/acceptance index and recovery documentation.

### 2. Complete the operator surface for features — completed before roadmap finalization

- Provide one supported CLI/Make target that can publish `market-basic` for an
  explicit security and `decision_at`.
- Require explicit normalized root, feature root, security ID, decision timestamp,
  computation delay, and Git commit resolution. Defaults may target local paths but
  must be visible in command output.
- Provide `inspect` or `verify` behavior that validates the manifest, part hashes,
  row count, input fingerprint, feature identity, and timing invariants without
  mutating the artifact.
- Emit machine-readable summaries suitable for later scheduling while keeping errors
  actionable for a human operator.
- Demonstrate idempotent republishing and immutable-identity conflict behavior.

The supported command landed in `e581c5d`, and its real-data replay evidence is
recorded by `b7dcac3`. Future work may extend the command only when a later version
introduces a new artifact or batch boundary.

### 3. Standardize raw preservation on parse failure

- Replace provider-specific top-level error behavior with a common result shape that
  can return downloaded resources and a parse/schema error together.
- Ensure every provider persists the exact successfully downloaded bytes and their
  response metadata before reporting a parsing failure.
- A top-level parse failure publishes no canonical rows or latest snapshots for the
  failed resource. Other independently valid resources may still make the run
  `partial` under the existing lifecycle rules.
- Add contract tests covering malformed top-level Yahoo, SEC, FRED, BCB, and CVM
  payloads, including compressed/CSV cases and multiple-resource responses.
- Verify the raw run manifest and stored hash by reading through `RawStore`, not by
  trusting filesystem paths.

### 4. Finish current research visibility

- Add an optional notebook section for manifest-backed CVM IPE filing inspection.
  Keep filings separate from the one-row price/fundamental/macro snapshot.
- Add an optional notebook section that reads an existing feature artifact and shows
  its feature version, `decision_at`, input availability, artifact availability, and
  input lineage.
- Keep the empty-checkout path executable and explanatory.
- Add fundamental and filing operational projections only if a concrete dashboard
  question requires them. If added, they remain latest-only and rebuildable.

### 5. Make recovery an accepted operation

- Document and script a backup set containing PostgreSQL, the raw tree, normalized
  manifests and listed parts, feature manifests and listed parts, configuration
  fingerprints, and the Git commit.
- Define restore ordering: restore immutable files, verify manifests and hashes,
  restore or rebuild PostgreSQL metadata/projections, then execute read-only catalog
  and dashboard checks.
- Add a reconciliation command/report for queued/running PostgreSQL runs, raw
  manifests with no terminal run, unlisted normalized parts, missing listed parts,
  and feature artifacts whose inputs are unavailable.
- Keep orphan cancellation explicit and reasoned. Recovery tooling may recommend an
  action but must not automatically cancel or delete evidence.
- Prove a restore into a clean temporary root. A backup that has not been restored is
  not accepted.

### 6. Establish an unattended daily runbook

- Define one host-level schedule for accepted daily sources using cron or systemd
  timers. Do not add Dagster/Prefect yet.
- Serialize or safely partition source runs so the same `(source, run_key)` cannot be
  accidentally launched twice.
- Generate stable date/source run keys, preserve effective inputs, and make retries
  operator-visible.
- Add alerts or a local failure summary for stale sources, failed/partial runs,
  projection lag, and disk consumption. Alert transport can remain local or
  log-based in v0.1.
- Define retention policy: raw and canonical evidence are retained; temporary files,
  obsolete unlisted parts, logs, and backups have explicit review-based policies.

## Data and migration work

No new bulk analytical store is required. Small additions may include:

- acceptance/reconciliation report schemas;
- optional PostgreSQL fields for scheduler identity or operator annotations, only if
  the run metadata cannot already represent them;
- no automatic orphan expiry and no deletion cascade over evidence.

Any migration must be justified by an operator query rather than by speculative
scheduler abstractions.

## Acceptance scenario

1. Restore or start from a clean clone and empty data roots.
2. Apply migrations and validate service health.
3. Collect a bounded AAPL/US macro/Brazil macro/CVM IPE slice with real source
   settings.
4. Verify raw run manifests by re-reading every object.
5. Verify canonical manifests and query them through DuckDB.
6. Execute the notebook from an empty state and again from the populated state.
7. Publish `market-basic`, verify it independently, republish identically, and prove a
   conflicting immutable identity is rejected.
8. Exercise one malformed live-like fixture per provider and confirm bytes survive
   while canonical output does not.
9. Back up the stack, restore it to a clean temporary location, and rerun catalog,
   feature, notebook, and dashboard checks.

## Required validation

- `make test`, `make notebook`, `make dashboard-smoke`, and image builds.
- Fresh migration apply, existing-volume upgrade, rollback where supported, and
  forward re-apply.
- `git diff --check` and schema fixture validation.
- Live acceptance report pinned to commit and effective run-input hashes.
- Recovery drill with manifest/hash comparison before and after restore.

## Suggested commit slices

1. Completed: `37e8812 docs: align feature and CVM status`
2. Completed: `e581c5d feat(features): add operator publication command`
3. `feat(provider): preserve downloaded resources on parse failure`
4. `feat(research): inspect filings and feature artifacts`
5. `feat(operations): reconcile durable ingestion state`
6. `docs(operations): define backup restore and daily runbooks`
7. `test(acceptance): certify the v0.1 foundation`

## Explicit non-goals

- New strategy signals or broad feature sets.
- Historical backtest claims.
- B3 market-data integration without its admission evidence.
- Distributed orchestration, object storage migration, multi-user auth, or live
  trading.

## Exit criteria

v0.1 is complete only when the clean-machine collection, feature publication,
failure-preservation, and restore acceptance scenario passes and the result is
recorded against an exact commit. A running local stack by itself is not enough.

---

# v0.2 — Historical Truth and Market Mechanics

## Goal

Create the minimum historically honest data layer required for serious daily or
weekly backtests. This is the most important correctness phase after the foundation.

## User outcome

The user can ask what price, macro release, identifier, universe membership,
corporate action, FX conversion, and filing information was valid and knowable at a
specified decision time for a bounded US and Brazilian universe.

## Entry criteria

- v0.1 recovery and unattended-ingestion gates pass.
- Every new source has an owner, terms/access review, request policy, captured
  fixtures, availability semantics, natural key, revision policy, and raw retention
  policy before implementation begins.
- Research code continues to reject datasets without a documented availability
  policy.

## Scope

### 1. ALFRED and macro vintages

- Add a dedicated ALFRED provider rather than overloading current FRED semantics.
- Preserve observation date, real-time/vintage start and end, release/publication
  evidence when present, local receipt time, series metadata version, and raw
  locator.
- Model revisions as separate canonical observations. An A -> B -> A revision history
  must remain three vintages, not collapse by value.
- Define deterministic selection for `decision_at`: choose the latest vintage whose
  availability is no later than the decision, then apply the existing total-order
  tie breakers.
- Build fixture and live acceptance around at least one revised series and one series
  with publication precision limitations.
- Keep current FRED pulls as current-vintage convenience data; do not silently relabel
  them historical.

### 2. Historical identity and universe membership

- Extend current identifier validity beyond YAML configuration into durable,
  source-backed history.
- Represent issuer-to-security relationships, listings, exchange/MIC, primary-listing
  status, currency, and identifier changes with validity intervals and provenance.
- Add index/universe membership intervals for at least one US benchmark and one
  Brazil benchmark or explicitly curated research universe.
- Preserve delisted and renamed securities. A current-universe lookup must never be
  the implicit backtest universe.
- Reject overlapping authoritative intervals for the same identifier or membership
  unless a documented source-priority rule resolves them.
- Provide as-of resolvers in Go/Python and test ticker changes, re-listings, duplicate
  tickers across exchanges, and delistings.

Suggested transactional records include:

```text
security_listing_versions
  security_id, exchange, mic, currency, primary_listing
  valid_from, valid_until, source_reference, recorded_at

universe_memberships
  universe_id, security_id, valid_from, valid_until
  source_reference, announced_at, available_at, recorded_at
```

The exact table names are less important than interval, availability, and provenance
invariants.

### 3. Trading calendars and decision clocks

- Introduce versioned exchange calendars for XNAS/XNYS and the selected B3 MIC.
- Represent sessions, holidays, early closes, and timezone explicitly.
- Define supported research clocks: after-close decision with next-session execution
  first; other clocks require separate policy.
- Never infer sessions by weekdays alone.
- Calendar revisions or corrections must be versioned and fingerprinted into later
  experiments.

### 4. Corporate actions and price bases

- Implement the existing corporate-action schema boundary for splits, reverse
  splits, cash dividends, stock dividends, ticker changes, mergers, spin-offs, and
  delistings in priority order.
- Preserve announcement, ex-date, record date, payment date, effective time,
  availability, currency, ratio/cash value, source ID, raw locator, and revision.
- Keep source raw prices and any source-adjusted series distinguishable.
- Define a reproducible adjustment artifact that records the exact raw price and
  corporate-action manifests used. Never overwrite raw prices with adjusted values.
- Start acceptance with splits and cash dividends; mergers/spin-offs can remain
  unsupported in simulation until their accounting rules are explicitly tested.
- Surface unsupported actions so a backtest fails or excludes the affected interval
  explicitly instead of producing a plausible but wrong return.

### 5. FX and multi-currency valuation

- Add daily FX observations for USD/BRL and the currencies required by the first
  global universe.
- Store quoted pair orientation, fixing/observation time, publication/availability,
  source, and precision.
- Define a canonical conversion rule and triangulation policy. Do not let callers
  guess whether a pair must be inverted.
- Later portfolio valuations must pin the exact FX inputs used.

### 6. SEC filing catalog and document identity

- Publish canonical SEC filing metadata separately from company facts.
- Preserve accession number, form, accepted/publication time, reporting period,
  amendment relationship, primary document, document URL, issuer identity, and raw
  lineage.
- Keep filing documents/attachments in raw storage where policy and capacity permit.
- Expose `filings_as_of` with the same strict knowledge cutoff as CVM filings.
- Do not parse narrative filing content into investment events yet; that belongs to
  v0.4.

### 7. Brazil admission path

- Treat B3 access as an explicit spike with a go/no-go result covering unattended
  access, redistribution/license constraints, stable URLs or APIs, captured fixtures,
  instrument identifiers, historical coverage, and rate limits.
- If admitted, add B3 instruments, daily quotes, indexes, and corporate actions behind
  the same raw-first/canonical interfaces.
- Expand BCB series only from a research question, with source metadata and bounded
  acceptance for each family.
- Keep CVM IPE canonical, CAD raw-only until a versioned issuer-snapshot contract is
  approved, and do not equate receipt time with historical public availability.
- If B3 is not admissible, document the decision and use an alternative licensed
  provider without weakening identifiers or provenance.

### 8. Source admission checklist for global expansion

World Bank, IMF, EIA, CFTC, commodity, and additional market-price data should not be
implemented as a provider checklist. Admit a source only when a planned research
question needs it and all of the following are defined:

- authoritative endpoint and terms;
- stable identity and natural key;
- revision/vintage behavior;
- publication and availability semantics;
- timezone/calendar behavior;
- pagination, retry, and rate-limit policy;
- raw retention and replay strategy;
- historical coverage and known gaps;
- fixture and live acceptance plan.

EIA is the likely first thematic expansion because it supports the AI-power and
energy research loop. A defensible commodity-price source is required before the
copper success scenario can claim historical evidence.

## Canonical contracts

Likely new or versioned schemas:

- `macro-vintage` or a backward-compatible extension to economic observations;
- `security-listing-version` and `universe-membership`;
- `trading-session`/calendar manifest;
- corporate actions and adjustment artifacts;
- FX observations;
- SEC filing metadata.

Every schema must state natural keys, revision behavior, nullable timestamp meaning,
precision, availability rules, provenance requirements, and whether it is valid for
historical backtesting.

## Research API additions

The Python catalog should gain explicit APIs such as:

```text
macro_vintage_as_of(series_id, decision_at)
security_identity_as_of(security_id, decision_at)
universe_as_of(universe_id, decision_at)
trading_session_at(mic, timestamp)
corporate_actions_as_of(security_id, decision_at)
adjusted_prices(security_id, decision_at, adjustment_policy_version)
fx_as_of(pair, decision_at)
filings_as_of(issuer_id, decision_at)
```

Names may change, but every method must require an explicit decision cutoff for
historical use. Present-day convenience methods must remain visibly separate.

## Acceptance scenarios

### US slice

- Select a historical benchmark membership date containing a security later renamed,
  delisted, or removed.
- Resolve its identifier and listing as of that date.
- Select price, split/dividend, ALFRED macro vintage, and SEC filing inputs available
  by a defined after-close decision.
- Prove that a later revision, filing, membership update, and corporate action are
  absent before their availability and present afterward.

### Brazil slice

- Resolve one B3-listed security and its currency/MIC as of a historical date using
  an admitted source or documented alternative.
- Select BCB/CVM/B3-or-alternative inputs under their explicit availability policies.
- Convert one valuation between BRL and USD using the pinned FX observation.
- Demonstrate a corporate action or explicitly mark it unsupported and block the
  affected simulation interval.

## Required validation

- Interval overlap and exclusion tests in PostgreSQL.
- Golden fixtures for vintage selection, identifier changes, calendars, actions, and
  FX orientation.
- Mutation tests around `decision_at` boundaries: one microsecond before, exactly at,
  and one microsecond after availability.
- Delisted-security and historical-universe bias tests.
- Adjustment arithmetic using exact decimals and independently checked examples.
- Live provider acceptance with raw/canonical hash verification.
- A written bias audit stating which datasets are backtest-safe, current-research
  only, or installation-replay only.

## Suggested commit slices

1. `docs(adr): define historical identity and universe semantics`
2. `feat(provider): collect ALFRED vintages`
3. `feat(data): publish historical security relationships`
4. `feat(data): publish exchange trading calendars`
5. `docs(adr): define corporate action adjustment policy`
6. `feat(data): publish corporate actions and adjustments`
7. `feat(data): add canonical foreign exchange observations`
8. `feat(data): publish SEC filing metadata`
9. `docs(data): record B3 source admission decision`
10. `test(acceptance): audit v0.2 point-in-time truth`

## Explicit non-goals

- A strategy framework, optimizer, or simulated brokerage.
- Narrative document extraction.
- Broad universe coverage before the two bounded acceptance slices are correct.
- Claiming survivorship-bias freedom without membership and delisting evidence.

## Exit criteria

v0.2 is complete when one bounded US slice and one bounded Brazil slice pass a
point-in-time bias audit that includes vintages, identifiers, membership, calendars,
corporate actions, and FX where relevant. Missing source access must be recorded as a
scope decision, not hidden behind a current-universe substitute.

---

# v0.3 — Research-Grade Feature Platform

## Goal

Turn the single-security `market-basic` implementation into a versioned, batch,
multi-dataset feature platform without turning the codebase into a dynamic feature
framework.

## User outcome

The user can request a feature snapshot for an explicit universe and decision
schedule, reproduce it from pinned manifests, compare feature versions, and inspect
why any row is null, stale, rejected, or unavailable.

## Entry criteria

- v0.2 identifies which canonical datasets are safe for historical selection.
- Trading calendars and adjustment policy are versioned.
- The single-artifact `market-basic` contract and verifier remain the reference
  implementation.

## Scope

### 1. Feature registry and contract discipline

- Keep a controlled registry of named feature sets. Do not discover arbitrary Python
  functions or infer schemas at runtime.
- Each feature set declares version, required input dataset contracts, entity type,
  decision frequency, lookback, calendar, null policy, computation delay, output
  schema, and implementation/generator version.
- A semantic change creates a new feature-set version. Existing artifacts remain
  readable by their original version or fail with an explicit unsupported-version
  error.
- Separate feature values from labels/future outcomes. Labels may share artifact
  mechanics later but must never be available to strategy code at decision time.

### 2. Dataset-wide batch runner

- Accept an explicit universe snapshot, security list, decision schedule, feature-set
  version, normalized root, output root, and computation policy.
- Resolve eligible inputs through `ResearchCatalog`, never direct provider calls or
  latest-only PostgreSQL projections.
- Partition work deterministically by feature set, decision date, and entity or
  bounded shard.
- Publish a dataset-level manifest that references all immutable parts and records
  the universe, calendar, input manifests, hashes, rejected entities, and run
  summary.
- Resume safely after interruption by verifying already-published artifacts and only
  computing missing deterministic partitions.
- Produce the same bytes or same semantic content hash for identical inputs and
  versions. Any unavoidable physical nondeterminism must be isolated and documented.

### 3. Feature artifact catalog

- Register small artifact metadata in PostgreSQL: artifact ID/version, feature set,
  decision range, universe fingerprint, input fingerprint, output manifest/hash,
  generator Git commit, status, and timestamps.
- Keep feature rows in Parquet. PostgreSQL is for discovery, lineage, run state, and
  operator queries only.
- Catalog registration occurs after immutable publication and is reconciliable if a
  process dies between stores.
- Expose lineage from a feature row to selected canonical manifests and from the batch
  to its universe/calendar definitions.

### 4. Initial feature families

Implement in dependency order and only where source quality supports them:

#### Market and risk

- total/raw return horizons: 1d, 1m, 3m, 6m, 12m;
- realized volatility and downside volatility;
- rolling maximum drawdown;
- relative strength versus an explicit benchmark;
- liquidity/turnover proxies where volume and shares are valid.

#### Fundamental growth and quality

- revenue, EPS, FCF, and capex growth;
- revenue and capex acceleration;
- gross/operating margins and changes;
- ROE, ROIC, leverage, and FCF conversion where taxonomy mapping is defensible.

#### Valuation

- price/sales, earnings yield, FCF yield, and EV-based ratios only after point-in-time
  shares, debt, cash, currency, and market-price inputs are available.
- Unknown or incomparable concepts remain null with a reason; they are not coerced
  through a fuzzy taxonomy match.

#### Macro and cross-asset

- yield-curve slopes and changes;
- inflation/growth/liquidity direction components;
- standardized changes and rolling percentiles using the selected vintage;
- FX and commodity sensitivity only after sufficient point-in-time histories exist.

Feature sets should be cohesive and versioned (`market-momentum`,
`fundamental-growth`, `valuation-basic`, `macro-state`, for example) rather than one
ever-growing object.

### 5. Taxonomy and comparability policy

- Define explicit mappings from SEC/CVM concepts to canonical research concepts,
  including scope, units, duration/instant type, sign, and issuer applicability.
- Prefer a small reviewed mapping registry over fuzzy matching.
- Version mappings and fingerprint them into feature inputs.
- Preserve unmapped facts for research inspection. A missing mapping is not a zero.
- Add issuer-specific overrides only with provenance, validity dates, review notes,
  and tests.

### 6. Feature inspection and data-quality reporting

- Provide coverage by universe/date/feature, null reasons, stale inputs, rejected
  values, and source contribution.
- Add notebook helpers for cross-sectional snapshots and time-series feature history.
- Add Grafana operational panels for feature run status and freshness, not factor
  performance claims.
- Permit a human to trace a surprising feature value to exact canonical rows and raw
  locators.

## Proposed durable contracts

- feature-set registry document;
- batch feature manifest;
- feature-computation run metadata;
- explicit null/rejection reason vocabulary;
- versioned taxonomy mapping registry;
- optional label-artifact ADR, but no labels need to ship in v0.3.

## Acceptance scenario

1. Select a historically valid 20–50 security universe and monthly decision schedule.
2. Publish market, one fundamental, and one macro feature set over at least two years.
3. Interrupt the run, resume it, and show no duplicate or conflicting committed
   partitions.
4. Re-run from the same manifests in a clean root and compare manifest hashes and
   row-level exact values.
5. Move `decision_at` across a filing or macro revision boundary and show only the
   eligible feature rows change.
6. Tamper with an input part, output part, registry version, taxonomy mapping, and
   universe membership; each case must fail closed.
7. Inspect coverage and explain at least three null/rejected cases from lineage.

## Required validation

- Unit/property tests for rolling windows, decimal arithmetic, null propagation, and
  minimum-history rules.
- Golden calculations checked against independent Pandas/DuckDB or hand-worked
  examples.
- Boundary tests for calendar sessions, corporate actions, vintage releases, and
  computation delay.
- Deterministic batch/resume/conflict/tamper tests.
- Performance budget measured on the accepted universe; optimize only after a
  documented bottleneck.

## Suggested commit slices

1. `docs(adr): define versioned feature-set registry`
2. `feat(features): publish batch feature manifests`
3. `feat(metadata): catalog feature artifacts`
4. `feat(features): add market momentum and risk set`
5. `feat(features): add reviewed fundamental mappings`
6. `feat(features): add growth and quality set`
7. `feat(features): add macro state set`
8. `feat(research): inspect feature coverage and lineage`
9. `test(acceptance): reproduce multi-asset feature datasets`

## Explicit non-goals

- User-authored plugin features, automatic feature discovery, or a feature store
  service.
- Strategy weights, buy/sell decisions, optimization, or ML.
- Publishing valuation features without point-in-time denominator evidence.

## Exit criteria

v0.3 is complete when a multi-asset, multi-date feature dataset can be published,
resumed, verified, reproduced in a clean root, and explained from raw lineage without
using any future or latest-only input.

---

# v0.4 — Theme Intelligence and Hypothesis Research Loop

## Goal

Support structured investment reasoning: connect evidence to themes and companies,
record a falsifiable hypothesis, and preserve predictions before their outcomes are
known.

## User outcome

The user can investigate a theme such as AI infrastructure, inspect linked companies
and indicators, record a thesis and invalidation conditions, freeze a dated
prediction, and later compare the observed outcome without rewriting history.

## Entry criteria

- v0.3 provides inspectable point-in-time features.
- SEC/CVM filing catalogs exist with stable document identity and raw references.
- At least one thematic macro/industry source has passed source admission.

## Scope

### 1. Relational theme model

- Use PostgreSQL tables, not a graph database.
- Represent themes, typed entities, hierarchical theme membership, and typed
  relationships such as `SUPPLIES`, `CUSTOMER_OF`, `DEPENDS_ON`, `BENEFITS_FROM`,
  `EXPOSED_TO`, `COMPETES_WITH`, `CONSUMES`, `PRODUCES`, and `INDICATOR_FOR`.
- Every edge carries confidence, direction, evidence reference, author/method,
  validity interval, recorded time, and revision state.
- Separate factual relationships from research interpretations. A supplier relation
  and a bullish exposure score are not the same assertion.
- Preserve revisions; never mutate a historical edge in place without an audit row.

### 2. One theme before generalization

Implement AI infrastructure as the reference theme:

```text
AI infrastructure
  -> compute
  -> semiconductor manufacturing and packaging
  -> memory
  -> networking
  -> datacenters and cooling
  -> generation, grid, and electrical equipment
```

- Start with a reviewed set of entities and relationships.
- Attach observable indicators and feature-set references to each branch.
- Include US/global companies and a small relevant Brazil exposure set where evidence
  supports it.
- Define what would weaken or invalidate each claimed relationship.
- Generalize the schema only after the reference theme reveals repeated needs.

### 3. Document catalog and immutable text extraction

- Store document identity and metadata separately from downloaded bytes and extracted
  text.
- Retain raw filing/report bytes, content hash, media type, source URL, publication
  time, retrieval time, issuer/entity links, and supersession/amendment relationships.
- Publish deterministic text-extraction artifacts with extractor name/version,
  configuration, page/section locators, input hash, output hash, and errors.
- Preserve tables and section anchors where practical so evidence can be reviewed in
  context.
- Never let a failed PDF/HTML parser erase or replace the raw document.

### 4. LLM-assisted structured events with review

- Use LLMs only to propose structured extractions from retained documents.
- Version the event schema, prompt/template, model identity, model parameters,
  extraction code, and source document hash.
- Require source spans/locators for every extracted claim.
- Store model output as a derived artifact, not canonical fact.
- Add validation and human review states: proposed, accepted, rejected, superseded.
- Initial event types should be narrow, such as capex guidance change, production
  guidance change, material customer/supplier mention, financing event, and stated
  theme exposure.
- Confidence is metadata, not permission to bypass review or source availability.

### 5. Append-only hypothesis ledger

Suggested records:

```text
hypotheses
  stable identity, title, status, created_at

hypothesis_revisions
  thesis, causal model, horizon, benchmark, universe
  invalidation conditions, decision_at, evidence snapshot

hypothesis_evidence
  evidence reference, direction, weight, note, available_at

predictions
  asset/universe, expected direction/range, horizon
  confidence, created_at, frozen revision

prediction_outcomes
  measurement policy, realized result, benchmark result
  drawdown, measured_at, input artifact references
```

- Corrections create revisions; they do not rewrite the original prediction.
- A prediction is immutable once marked frozen/active.
- Outcome computation uses a versioned measurement policy and separately records
  unavailable or invalid outcomes.
- The ledger records human uncertainty and reasoning; it is not a social feed or
  auto-generated investment recommendation system.

### 6. Research workspace

- Extend Jupyter helpers to assemble a dated evidence pack: theme graph slice,
  canonical observations, feature snapshots, filings/documents, and existing
  hypotheses.
- Provide commands to create, revise, freeze, and close a hypothesis using validated
  documents or a small local API. Do not build a large frontend.
- Add a read-only Grafana or lightweight report view for active hypotheses, upcoming
  review dates, evidence freshness, and prediction outcomes.
- Export a self-contained Markdown/JSON research memo with artifact IDs and lineage.

## Acceptance scenario

1. Create the AI-infrastructure theme and reviewed relationship set.
2. Select a decision date and generate a point-in-time evidence pack using only
   eligible macro, company, feature, and filing inputs.
3. Extract one narrow event from a retained filing with a source locator, reject one
   bad proposal, and accept one correct proposal.
4. Record a causal thesis, benchmark, horizon, and explicit invalidation conditions.
5. Freeze predictions for a bounded set of securities.
6. Attempt to edit the frozen prediction and verify a new revision is required.
7. Advance the measurement date, compute outcomes under the pinned policy, and trace
   every value back to manifests.

## Required validation

- Database constraints for relationship validity, revision history, and immutable
  frozen predictions.
- Prompt/model/text-extractor fixture tests that do not require network access.
- Source-span and document-hash verification.
- Point-in-time tests ensuring a later filing or extracted event is absent from an
  earlier evidence pack.
- Human-review authorization tests, even in a single-user local deployment.
- Export/re-import round trip for a research memo and its referenced IDs.

## Suggested commit slices

1. `docs(adr): define themes evidence and hypothesis revisions`
2. `feat(metadata): add relational theme records`
3. `feat(documents): publish versioned text artifacts`
4. `feat(documents): propose reviewed structured events`
5. `feat(research): add append-only hypothesis ledger`
6. `feat(research): build point-in-time evidence packs`
7. `feat(observability): show active research commitments`
8. `test(acceptance): close one hypothesis research loop`

## Explicit non-goals

- An autonomous LLM stock picker or automatic order generation.
- A generalized knowledge graph platform.
- Bulk ingestion of every news source or unlicensed document corpus.
- Treating model confidence as ground truth.

## Exit criteria

v0.4 is complete when one theme-backed thesis can be reconstructed exactly as it was
known at its decision date, its frozen prediction cannot be rewritten, and its later
outcome is calculated from pinned data and policy versions.

---

# v0.5 — Point-in-Time Backtesting and Experiment Tracking

## Goal

Build a transparent daily-frequency simulator that tests simple strategies without
lookahead or survivorship bias and records enough state to reproduce every result.

## User outcome

The user can define a baseline strategy over a historical universe, run it with
realistic calendars, actions, FX, execution delay, and costs, compare it with a
benchmark, inspect trades and failures, and reproduce the result months later.

## Entry criteria

- v0.2 historical-truth gates pass for the selected universe and period.
- v0.3 feature artifacts are reproducible and expose availability.
- The strategy input path cannot call present-day convenience APIs or provider
  clients.

## Scope

### 1. Backtest contract and experiment identity

Define a strict versioned experiment specification containing:

- strategy name/version and Git commit;
- parameter document and canonical parameter hash;
- universe definition/version and membership fingerprint;
- decision schedule and trading-calendar version;
- feature, price, action, FX, and benchmark manifest references;
- signal delay and execution timing policy;
- initial cash, base currency, rebalancing rule, and fractional-share policy;
- fee, spread, slippage, tax, and borrow assumptions;
- portfolio/risk constraints;
- development, validation, out-of-sample, and forward-paper date partitions;
- random seed where randomness is explicitly used.

The experiment ID should be deterministic from the immutable specification. Run IDs
may differ for operational retries, but identical completed experiments must compare
as equivalent.

### 2. Daily event loop

Implement a simple, inspectable simulator before evaluating LEAN:

```text
load session and information available by decision_at
  -> compute/read signal
  -> construct target exposure
  -> apply portfolio and risk constraints
  -> create proposed orders
  -> execute under next-session policy
  -> apply fills, fees, FX, and corporate actions
  -> mark holdings and cash
  -> persist audit events and metrics
```

- Use exchange sessions, not calendar days.
- The first execution policy should be unambiguous, such as after-close decision and
  next-session open or close with explicit delay.
- If required price/action/FX data is missing, apply a documented halt/reject policy;
  do not silently forward-fill execution prices.
- Unsupported corporate actions block or explicitly quarantine affected securities.
- Preserve orders, fills, cash movements, holdings, valuations, and reasons as an
  append-only simulation ledger.

### 3. Strategy interface

Separate signal generation from portfolio construction. A strategy consumes a
point-in-time research context and emits scores or desired exposures, never broker
orders.

Initial baselines:

- buy-and-hold benchmark;
- equal-weight periodic rebalance;
- 6–12 month momentum with an explicit skip period;
- simple value or quality ranking where data coverage supports it;
- a transparent multi-factor rank.

Every later strategy, including thematic or ML work, must beat relevant simple
baselines after costs and under the same universe/data policy.

### 4. Costs and market mechanics

- Implement configurable fixed/percentage commissions, spread, slippage, minimum
  fees, and execution delay.
- Keep US and Brazil fee/tax profiles separate and versioned.
- Use conservative volume/participation constraints or reject trades when liquidity
  evidence is insufficient.
- Support cash dividends, splits, delistings, and FX conversion at minimum before
  claiming total return.
- Model taxes only to the fidelity necessary for the intended account and clearly
  label omissions. Incorrectly detailed tax logic is worse than an explicit pre-tax
  result.

### 5. Metrics and attribution

Publish immutable result artifacts containing:

- daily NAV, cash, gross/net exposure, positions, orders, and fills;
- CAGR, annualized volatility, Sharpe, Sortino, max drawdown, Calmar, beta, alpha,
  turnover, win rate, profit factor, and benchmark-relative return;
- performance by year, sector, industry, country, macro regime, and volatility regime
  where classification data is point-in-time valid;
- cost, FX, dividend, and corporate-action contribution;
- missing-data/rejected-trade counts and time out of market.

Metric definitions, annualization factors, risk-free series, benchmark, and missing
period rules must be versioned. Store exact series and inputs, not only summary
numbers.

### 6. Experiment catalog and comparison

- Register experiment specifications, result manifests, status, start/end time,
  environment versions, and validation partition in PostgreSQL.
- Add CLI/notebook comparison by strategy, parameter set, universe, costs, and period.
- Make development/validation/out-of-sample labels visible and immutable after a run
  is promoted.
- Record the number of attempts against a holdout. Do not allow repeated tuning to be
  presented as untouched out-of-sample evidence.

### 7. Walk-forward evaluation

- Add rolling train/calibration/test windows only after fixed baseline runs pass.
- Feature selection or parameter calibration uses only the training window.
- Any scaler, winsorization bound, neutralization, or imputation model is fitted
  inside the window and versioned as an artifact.
- Aggregate walk-forward results without erasing individual window failures.

### 8. LEAN integration decision

Run a bounded engineering spike after the internal daily simulator is accepted:

- map one platform feature/signal artifact and one universe into LEAN;
- reproduce a baseline under equivalent timing and cost assumptions;
- compare corporate-action, calendar, brokerage, and result semantics;
- measure operational cost and coupling.

Adopt LEAN only as a replaceable simulation/brokerage adapter if it materially
improves fidelity. Do not change canonical data or feature contracts to mirror LEAN
internals.

## Bias test suite

The suite must intentionally fail when:

- a feature becomes available after the decision time;
- a revised macro value is selected before its vintage;
- a later filing or theme event leaks into an earlier decision;
- current universe membership replaces historical membership;
- a delisted security disappears from the run;
- an adjusted price and dividend are both counted;
- next-session execution uses the decision-session close without permission;
- future FX is used for conversion;
- holdout transformations are fitted on the full sample.

## Acceptance scenario

1. Run buy-and-hold, equal-weight, and momentum baselines over a bounded historically
   valid US universe.
2. Run at least one suitable Brazil baseline with BRL accounting and optional USD
   reporting.
3. Compare zero-cost and conservative-cost results and explain the difference from
   orders/fills.
4. Include a split, dividend, delisting/removal, holiday, and macro revision in the
   covered fixtures or period.
5. Re-run the same experiment in a clean environment and compare specification,
   result manifest, NAV, orders, fills, and metrics.
6. Perturb one input manifest and prove a new experiment identity is required.
7. Run the bias suite and retain the report with the acceptance evidence.

## Required validation

- Unit tests for accounting identities: cash plus marked positions equals NAV within
  exact policy tolerances.
- Golden trade-ledger scenarios checked by hand.
- Property tests for splits/dividends, zero trades, no data, negative/zero cash rules,
  and multi-currency conversion.
- Independent metric checks against a trusted Python library for representative
  series, while retaining local formula definitions.
- Deterministic replay, crash/resume, and artifact tamper tests.
- Performance budget for the accepted universe and horizon.

## Suggested commit slices

1. `docs(adr): define backtest clocks and experiment identity`
2. `feat(backtest): publish experiment specifications`
3. `feat(backtest): simulate daily orders fills and cash`
4. `feat(backtest): apply actions costs and foreign exchange`
5. `feat(strategies): add transparent baseline signals`
6. `feat(backtest): publish metrics and attribution`
7. `feat(research): compare immutable experiments`
8. `test(backtest): enforce historical bias boundaries`
9. `docs(backtest): record LEAN integration decision`
10. `test(acceptance): reproduce v0.5 baseline experiments`

## Explicit non-goals

- Intraday/tick simulation, options, margin, short borrow modeling beyond a proven
  need, or smart-order routing.
- Parameter sweeps over an untouched holdout.
- Deep learning, reinforcement learning, or LLM-generated trades.
- Real or paper broker submission.

## Exit criteria

v0.5 is complete when baseline US and Brazil experiments reproduce from immutable
inputs, the accounting ledger balances, conservative costs and actions are applied,
and the deliberate lookahead/survivorship failures are caught by tests.

---

# v0.6 — Portfolio Construction and Paper Trading

## Goal

Use the accepted research and backtest contracts in a forward-only daily process that
creates constrained target portfolios, simulates orders/fills, and measures live
paper performance without risking capital.

## User outcome

The user can activate a versioned strategy for a paper account, review the evidence
and proposed rebalance, approve or reject it, reconcile simulated fills and holdings,
and compare forward results with the historical expectation.

## Entry criteria

- v0.5 baseline experiments and bias suite pass.
- At least one strategy has frozen parameters before the paper start date.
- Daily ingestion and feature jobs meet freshness requirements for the selected
  decision clock.

## Scope

### 1. Portfolio construction boundary

Keep four interfaces separate:

```text
signal scores
  -> target exposures
      -> risk-validated desired positions
          -> proposed orders
              -> paper broker fills
```

- A strategy never submits orders.
- The constructor records objective, constraints, current holdings, prices, FX,
  turnover penalty, and fallback behavior.
- Start with deterministic rank-weighted/equal-weight construction before numerical
  optimization.
- If an optimizer is introduced, pin solver/library versions and reject infeasible or
  numerically unstable results.

### 2. Risk policy

Version and enforce:

- maximum position, sector, country, currency, and theme exposure;
- maximum gross/net exposure and turnover;
- minimum cash reserve;
- stale/missing data and price-gap halts;
- liquidity/participation bounds;
- prohibited or unsupported instruments;
- maximum single-day proposed notional change;
- drawdown or operational circuit breakers.

Risk failures produce explanations and no orders. There is no override hidden inside
strategy code.

### 3. Paper account ledger

- Model accounts, base currency, cash balances, positions, tax lots if required,
  orders, order states, fills, fees, dividends, corporate actions, transfers,
  valuations, and reconciliations.
- Use an append-only double-entry or equivalently auditable ledger. Derived current
  positions may be projected for speed but must be rebuildable.
- Give every proposed order a deterministic client identity derived from account,
  decision, strategy version, and target revision.
- A repeated daily job must not duplicate an order or cash movement.
- Keep backtest and paper event semantics aligned so differences are intentional and
  reportable.

### 4. Daily paper cycle

```text
verify source freshness and completed manifests
  -> freeze decision input snapshot
  -> publish/read features
  -> generate signal artifact
  -> construct target portfolio
  -> run risk checks
  -> present proposed orders and evidence
  -> explicit paper approval or policy-approved simulation
  -> simulate fills under the declared clock
  -> reconcile ledger and publish daily report
```

- Freeze the exact input snapshot before proposals are displayed.
- If later data arrives, it belongs to a new decision revision; it does not mutate the
  approved one.
- v0.6 may auto-approve paper orders under a recorded policy, but the UI must retain a
  manual-review mode because the same boundary will protect any later live path.

### 5. Forward-performance evaluation

- Compare paper NAV, turnover, costs, exposures, missing-data events, and fills with
  the promoted backtest expectation.
- Track prediction calibration and hypothesis outcomes independently from portfolio
  P&L.
- Record operational misses: late source, stale feature, skipped rebalance, rejected
  risk check, manual rejection, and reconciliation difference.
- Never splice paper results into historical results as if they were one backtest.

### 6. Optional broker sandbox adapter

- First deliver an internal paper broker using the same execution policy as v0.5.
- Evaluate Alpaca or Interactive Brokers paper/sandbox only if it improves operational
  realism or is a likely future brokerage.
- Keep broker DTOs behind an adapter. Canonical instruments, target portfolios, and
  experiment artifacts must not adopt broker-specific identifiers as primary keys.
- Store secrets outside Git, use least privilege, redact logs, and support an explicit
  disabled mode.
- Reconcile remote orders/fills/positions into the local ledger; the broker response
  is evidence, not permission to overwrite history.

### 7. Operator views

- Daily review report: data freshness, strategy/version, input snapshot, target versus
  current positions, proposed trades, expected costs, constraint usage, and warnings.
- Portfolio dashboard: NAV, cash, exposure, drawdown, benchmark, holdings, sector/
  country/currency/theme concentration, turnover, and rejected/skipped decisions.
- Audit view: why a target changed, which evidence and feature artifacts were used,
  who/what approved it, and how fills changed the ledger.
- Keep Grafana for operational and portfolio monitoring; use notebook/Markdown reports
  for deeper research explanation instead of forcing all analysis into dashboards.

## Acceptance scenario

1. Create a paper account and fund it with a recorded cash event.
2. Activate frozen equal-weight and one factor strategy in separate accounts or
   sleeves.
3. Execute at least 20 trading sessions using recorded/replayed daily decisions, with
   one rebalance, one no-op day, one stale-data halt, one risk rejection, one dividend,
   and one corporate action.
4. Restart the service between proposal and fill; resume without duplicate orders.
5. Rebuild projected positions and NAV from the ledger and compare exactly.
6. Restore the paper state from backup and reconcile all balances.
7. If a broker sandbox is used, reconcile remote and local orders/fills and surface an
   intentionally injected discrepancy.

## Required validation

- Ledger balance and event-idempotency tests.
- Risk-policy table tests for every constraint and combined constraints.
- Golden construction examples, infeasible portfolio cases, and rounding/odd-lot
  behavior.
- Restart, duplicate delivery, delayed fill, rejected/cancelled order, and stale-data
  scenarios.
- Daily report snapshot tests and dashboard SQL smoke tests.
- Forward-paper acceptance report with strategy versions frozen before the period.

## Suggested commit slices

1. `docs(adr): define portfolio and paper ledger boundaries`
2. `feat(portfolio): construct deterministic target exposures`
3. `feat(risk): validate proposed portfolios`
4. `feat(paper): persist accounts orders fills and cash`
5. `feat(paper): run idempotent daily decisions`
6. `feat(observability): add paper portfolio dashboards`
7. `feat(paper): reconcile immutable account history`
8. `test(acceptance): recover and replay v0.6 paper accounts`

## Explicit non-goals

- Real-money order submission.
- Leverage, margin, options, futures, shorting, or intraday execution.
- A black-box optimizer or strategy-controlled risk overrides.
- Replacing the local audit ledger with broker current-state responses.

## Exit criteria

v0.6 is complete when the forward paper cycle can run daily, explain and reproduce
each decision, reject unsafe/stale inputs, recover without duplicate effects, and
rebuild all holdings and cash from the audit ledger.

---

# v1.0 — Full Personal Research Platform

## Goal

Harden and accept the complete collect-to-paper loop for regular personal use by
April 2027. v1.0 is a product integration and operational-trust version, not a wave
of unrelated new features.

## User outcome

The user can start with a market or thematic question, retrieve dated evidence,
record a hypothesis, backtest an explicit signal, promote a frozen version to paper,
review proposed portfolio changes, and measure forward outcomes from one documented
local platform.

## Entry criteria

- v0.1 through v0.6 hard gates are accepted for their bounded scopes.
- At least one paper strategy has a forward record; lack of time cannot be replaced
  by historical simulation.
- All known data sources are labeled by historical fitness and operational health.

## Scope

### 1. End-to-end release contract

- Pin supported Go/Python/PostgreSQL/DuckDB/Parquet/Grafana versions and container
  images.
- Define schema, migration, feature-set, strategy, experiment, risk-policy, and ledger
  compatibility for the v1 line.
- Add a version manifest that records these components and rejects unsupported mixes.
- Provide a documented upgrade path from the accepted v0 foundation without
  rewriting immutable history.
- Generate a release evidence index with commands, results, known limitations, and
  restore proof.

### 2. Product workflows

Support and document these workflows end to end:

#### Ask and retrieve

- Search a bounded security/theme/macro catalog.
- Inspect current state and an explicit historical `decision_at`.
- See data fitness, freshness, lineage, and missing coverage before drawing a
  conclusion.

#### Form a hypothesis

- Assemble an evidence pack.
- Record causal model, horizon, benchmark, predictions, and invalidation conditions.
- Freeze the dated revision.

#### Backtest and compare

- Select eligible features/universe/periods.
- Run or retrieve a reproducible experiment.
- Compare baselines, costs, regimes, and validation partitions.

#### Promote and paper trade

- Promote only a frozen strategy/parameter/risk-policy bundle.
- Review target changes and proposed orders.
- Reconcile fills, exposures, P&L, and operational deviations.

#### Measure and learn

- Close predictions and hypotheses without rewriting their original claims.
- Compare forward paper evidence with backtest assumptions.
- Record the next research question as a new revision or experiment.

### 3. Data coverage for the first real research loops

v1.0 should prefer depth and trust over a nominal worldwide provider count. Minimum
coverage should include:

- US and selected global daily equities for a curated research universe;
- first-class Brazil daily equity/index coverage through an admitted provider;
- SEC fundamentals and filing metadata;
- CVM filing metadata and BCB macro data;
- revised US macro via ALFRED plus selected global/Brazil macro series;
- exchange calendars, corporate actions, benchmark membership, and FX;
- the thematic sources required by the AI-infrastructure reference theme;
- one defensible commodity/industrial-activity path if the copper research scenario
  is included in acceptance.

Every dataset remains labeled as backtest-safe, current-research only, or
installation-replay only.

### 4. Operational hardening

- Run daily collection, feature, decision, paper, backup, and reconciliation jobs
  through the simplest reliable host scheduler.
- Add bounded concurrency, explicit dependencies, retry budgets, and observable run
  identities without introducing a distributed queue.
- Define service/data health objectives: freshness by source, job completion window,
  maximum unresolved partial runs, backup age, restore time, disk headroom, and paper
  reconciliation tolerance.
- Add alert routing only where the operator will actually receive it.
- Perform disk-full, network-failure, provider-schema-change, PostgreSQL restart,
  partial-manifest, stale-data, and clock/timezone drills.
- Document capacity thresholds that would justify MinIO/S3, an orchestrator, or
  partition redesign. Do not migrate merely because those tools are available.

### 5. Security and privacy

- Keep all network services loopback-only by default.
- Put provider/broker credentials in local secret storage with minimum permissions;
  never in config committed to Git, manifests, notebooks, or logs.
- Add authentication before any non-loopback or multi-user exposure.
- Record sensitive-data classification and backup handling.
- Scan release artifacts/logs for secrets and personal contact leakage.
- Document SEC User-Agent contact handling separately from secrets.

### 6. Usability without a frontend project

- Provide a concise command reference for setup, migrate, collect, verify, research,
  feature, backtest, paper, reconcile, backup, restore, and acceptance.
- Provide notebook/report templates for market review, company review, theme evidence,
  hypothesis, experiment comparison, and paper review.
- Make error messages identify the failed contract, source/artifact, safe next action,
  and whether any durable evidence was published.
- Use Grafana for health and current monitoring. Build a custom UI only if repeated
  operator workflows remain materially painful after CLI/notebook/report refinement.

### 7. Documentation set

The v1 docs should include:

- architecture and storage truth;
- data-source registry and historical-fitness matrix;
- timestamp, vintage, identity, calendar, action, and FX semantics;
- feature registry and taxonomy mapping policy;
- theme/document/hypothesis contracts;
- backtest clock, costs, metrics, and bias controls;
- portfolio/risk/paper ledger semantics;
- operator setup, daily runbook, reconciliation, backup, restore, and incident guides;
- version compatibility and upgrade guide;
- limitations and explicitly unsupported instruments/actions.

## v1.0 acceptance scenarios

### Scenario A: thematic question to paper decision

1. Ask whether AI-infrastructure demand is accelerating and which reviewed companies
   have improving fundamentals and relative momentum.
2. Build a dated evidence pack using theme relationships, macro/industry observations,
   filings, and feature artifacts.
3. Record and freeze a hypothesis with invalidation conditions.
4. Backtest a transparent related factor against boring baselines and conservative
   costs.
5. Promote a frozen version to a paper sleeve.
6. Review and simulate proposed orders.
7. Measure forward paper and hypothesis outcomes without altering the original thesis.

### Scenario B: cross-market/commodity question

1. Ask how an industrial/commodity acceleration relates to selected global and Brazil
   companies.
2. Resolve historical universe, identifiers, FX, macro vintages, actions, and company
   evidence as of a date.
3. Run a point-in-time comparison with US, sector, country, and simple factor
   benchmarks.
4. Explain missing or current-only data rather than silently backfilling it.

### Scenario C: disaster recovery

1. Stop the stack after a simulated partial provider failure and interrupted feature
   or paper job.
2. Restore the latest accepted backup into a clean root.
3. Verify raw, canonical, feature, experiment, and paper-ledger integrity.
4. Reconcile unfinished work and resume without duplicate canonical parts, orders,
   fills, or cash events.
5. Rebuild PostgreSQL projections and Grafana views from durable truth where designed.

### Scenario D: historical-bias challenge

Run the deliberate leakage/survivorship test suite against the release and retain its
report. The release fails if any strategy path can access latest-only projections,
future features, later vintages, current-only membership, or double-adjusted returns.

## Release-quality gates

- All repository validation targets pass from a clean checkout.
- Fresh install, upgrade, backup, restore, and interrupted-run recovery pass.
- Live bounded-source acceptance passes with exact configuration/input fingerprints.
- All canonical and derived manifests verify; no reader depends on unlisted files.
- Data-fitness matrix is complete for every dataset reachable by backtest code.
- Baseline experiment reproduction and bias suite pass.
- Paper ledger reconstruction and idempotency pass.
- Secret scan and loopback-default checks pass.
- Known limitations are specific enough that a user knows which conclusions are not
  supported.

## Suggested integration slices

1. `docs(version): define the v1 compatibility contract`
2. `feat(operations): orchestrate the local daily research cycle`
3. `feat(operations): enforce release health objectives`
4. `feat(research): add complete workflow reports`
5. `docs: publish v1 operator and research guides`
6. `test(acceptance): exercise v1 end-to-end scenarios`
7. `chore(release): record v1.0 evidence and limitations`

## Explicit non-goals

- Autonomous real-money execution.
- A promise of complete worldwide security or alternative-data coverage.
- Intraday/tick strategies, options, futures, leverage, shorting, tax automation, or
  personalized legal/tax advice.
- A multi-tenant SaaS product, mobile app, or Bloomberg-equivalent UI.
- ML as a release requirement. A reliable simple-factor system is a successful v1.

## Exit criteria

v1.0 is complete when the user can execute the collect -> observe -> hypothesize ->
test -> simulate -> measure -> learn loop from documented commands, reproduce each
claim from immutable evidence, and recover the platform without losing or duplicating
durable state.

---

# Post-v1 — Optional Manually Approved Live Execution

This is a conditional future version, not part of the April 2027 v1.0 commitment.
Beginning to invest in April 2027 can mean using the platform for research and
manually placing trades at a brokerage. Direct integration should wait for evidence,
not the calendar.

## Entry gates

All gates are mandatory:

- at least three to six months of stable forward paper operation for the promoted
  strategy and intended decision frequency;
- no unresolved ledger discrepancies and a sustained reconciliation-clean period;
- documented comparison of paper assumptions with intended broker behavior;
- successful backup/restore and duplicate-order recovery drills;
- explicit maximum capital/notional and loss limits;
- manual review of strategy, risk policy, broker permissions, and legal/tax needs;
- separate operator decision to enable live mode.

## Initial scope

- One cash account, one broker, long-only supported equities, and a bounded whitelist.
- No margin, options, futures, shorting, leverage, or unattended strategy changes.
- Strategy emits targets; portfolio/risk emits proposed orders; only a dedicated
  broker adapter can submit them.
- Every batch requires manual approval after showing evidence, current holdings,
  proposed orders, expected costs, limit/notional checks, and stale-data status.
- Use deterministic client order IDs, idempotent submit/cancel handling, broker status
  polling, local append-only audit events, and post-trade reconciliation.
- Provide global kill switch, per-strategy disable, maximum order/notional/day limits,
  price collars, market-hours checks, and credential revocation instructions.
- Fail closed on unknown broker state, stale inputs, reconciliation mismatch, clock
  skew, duplicate intent, or unsupported action.

## Acceptance before capital

- Exercise the exact broker adapter against sandbox/paper endpoints.
- Inject timeouts before and after submission to prove duplicate orders are avoided.
- Inject partial fills, rejects, cancels, corporate actions, and broker outages.
- Reconcile every remote event to the local ledger and rebuild current state.
- Run a final dry-run mode that generates the exact intended live requests without
  submitting them.
- Start with a separately approved minimal notional and retain manual approval.

Autonomous live trading, if ever desired, requires another version and another safety
review after substantial live evidence. It must not emerge as a configuration change
inside the initial adapter.

---

# Cross-Version Workstreams

## Data-source priority

Source work should be pulled by research needs in this order:

1. Maintain and verify SEC, Yahoo/replacement, FRED/ALFRED, BCB, and CVM.
2. Admit corporate-action, calendar, universe-membership, FX, and B3-or-alternative
   sources needed for honest US/Brazil simulation.
3. Add EIA and a small set of global/commodity series required by the reference theme
   and product acceptance questions.
4. Add World Bank/IMF for global structural context when a concrete notebook or
   feature consumes them.
5. Add CFTC only when a futures-positioning research hypothesis exists.

Provider count is not a success metric. Coverage, historical fitness, freshness,
replayability, and actual use in a research loop are.

## Data fitness matrix

Maintain one machine-readable and documented registry with at least:

| Field | Meaning |
| --- | --- |
| dataset/source/version | Exact contract being classified |
| authority and access | Origin, terms, credentials, and unattended-access status |
| coverage | Entities, fields, dates, frequency, and known gaps |
| time semantics | Observed, published, available, effective, vintage, precision |
| revision policy | Append, replace, current snapshot, or unknown |
| historical fitness | Backtest-safe, bounded-safe, current-only, installation replay |
| identity policy | Natural key and mapping source |
| quality checks | Validation, expected cadence, missingness, discontinuity rules |
| evidence | Fixture/live acceptance commit and archive |
| consumers | Catalog APIs, features, notebooks, dashboards, strategies |

Backtest code should require an eligible classification, not rely on a comment or
caller discipline.

## Schema and ADR sequence

Likely ADR topics, numbered at implementation time to avoid collisions:

1. historical identities, listings, and universe membership;
2. exchange calendars and decision clocks;
3. corporate actions and reproducible adjustment;
4. data-source historical-fitness classification;
5. controlled feature-set registry and batch artifacts;
6. theme evidence and hypothesis revision history;
7. document extraction and reviewed derived events;
8. backtest experiment identity, execution clock, and accounting;
9. portfolio construction, risk policy, and paper ledger;
10. optional broker execution and manual approval.

Each ADR should state context, decision, invariants, rejected alternatives,
consequences, migration, and tests. It should not merely restate code after the fact.

## Testing layers

Every version should preserve the same evidence ladder:

1. **Schema tests:** strict fixtures, unknown fields/versions, duplicate keys, temporal
   order, exact decimals, and provenance.
2. **Unit/property tests:** parsers, natural keys, interval selection, calculations,
   accounting, and failure cases.
3. **Component tests:** RawStore, manifest publication, PostgreSQL transactions,
   reconciliation, and artifact readers.
4. **Integration tests:** collector -> raw -> canonical -> research -> derived artifact
   across service boundaries.
5. **Migration tests:** fresh up, upgrade, down/restore, and idempotent re-apply.
6. **Notebook/dashboard tests:** empty and populated behavior plus strict SQL planning.
7. **Live bounded acceptance:** real provider bytes, effective inputs, hashes, row
   counts, rejects, retries, and availability boundaries.
8. **Recovery tests:** interruption, backup/restore, orphan/unlisted reconciliation,
   and duplicate-effect prevention.
9. **Bias/safety tests:** deliberate lookahead, survivorship, double adjustment, stale
   data, risk rejection, and order idempotency failures.

## Observability evolution

Add operational visibility with the owning version:

- v0.1: source freshness, failures/partials, disk, manifests, backups, reconciliation.
- v0.2: vintage lag, identity/calendar/action coverage, source-fitness gaps.
- v0.3: feature batch status, coverage, null/reject reasons, input freshness.
- v0.4: document extraction/review queues and hypothesis review dates.
- v0.5: experiment status, rejected data intervals, runtime, and reproducibility.
- v0.6: paper decisions, risk rejections, order/fill/reconciliation status, exposure.
- v1.0: end-to-end daily-cycle objectives, restore age, and release compatibility.

Grafana panels should answer an operator question and expose missing state. Avoid
high-cardinality row dumps or dashboards that become an alternate research database.

## Performance and scale triggers

Do not add infrastructure preemptively. Record measurements and use explicit triggers:

- Move filesystem raw/artifact storage to MinIO/S3 only when backup, capacity,
  integrity, or multi-host access is materially better and the RawStore/manifest
  contracts remain unchanged.
- Add Dagster/Prefect only when host scheduling cannot express dependencies,
  retries, backfills, and observability without repeated bespoke logic.
- Add a queue only when measured work concurrency or isolation cannot be safely
  handled by bounded local workers.
- Repartition Parquet only after query profiles identify a concrete scan/file-count
  problem.
- Evaluate ClickHouse only for an actual operational analytical workload not served
  by DuckDB/Parquet and PostgreSQL projections.

No trigger in this roadmap implies Kafka, Spark, or Kubernetes.

## Security baseline

At every version:

- bind services to loopback by default;
- never commit secrets or emit them into run manifests;
- use descriptive but non-secret SEC contact configuration;
- redact provider/broker headers and tokens from logs and raw metadata;
- validate paths and avoid provider-controlled filesystem names;
- bound downloads, decompression, rows, execution time, and retries;
- treat documents and model-extracted text as untrusted input;
- verify hashes before consuming restored or transferred artifacts.

## Documentation and handoff standard

Each accepted version should leave:

- exact commit and compatibility versions;
- what is authoritative versus a projection;
- completed scope and explicit non-goals;
- commands to configure, migrate, run, verify, back up, restore, and troubleshoot;
- acceptance evidence and known limitations;
- next smallest cohesive units, not a vague feature wishlist;
- clean separation between tracked docs and external large evidence archives.

## Definition of done for any version

A version is not done because its happy path runs once. It is done when:

- semantics are recorded before or with implementation;
- strict contracts and migrations exist;
- unit, component, integration, and failure tests pass;
- operator and research workflows are documented;
- live or realistic acceptance is pinned to an exact commit and input configuration;
- interruption/retry behavior is demonstrated;
- durable artifacts can be verified and recovered;
- missing or unsupported cases fail visibly;
- the exit criteria in that version are met without borrowing claims from later work.

## First execution queue

The immediate queue after approving this roadmap should remain narrow. The original
feature-documentation and operator-command actions are already accepted, so the
remaining queue begins at the next data-integrity boundary:

1. Standardize downloaded-bytes-plus-parse-error behavior across providers.
2. Add filing and feature inspection to the notebook without changing snapshot
   cardinality.
3. Add reconciliation and backup/restore runbooks, then execute a clean restore drill.
4. Certify the remaining v0.1 scope with a bounded live acceptance.
5. Finish and accept ALFRED vintage ingestion without treating in-progress work as
   complete before its tests and live boundary pass.
6. Write the historical-identity/universe and calendar ADRs before the rest of v0.2.
7. Add the first v0.2 vintage-selection and historical-bias fixtures.

Do not start the general backtester while any of steps 1–7 remain unaccepted. The
fastest path to the full platform is to keep every later result explainable from a
trusted historical input boundary.
