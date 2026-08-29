# ADR 0010: Versioned feature-set registry

- Status: Accepted
- Date: 2026-08-29

## Context

The first feature artifact is a deliberately closed `market-basic` implementation.
Its constants and validators keep one security, one decision timestamp, and four
features reproducible, but they do not provide a durable description that a batch
runner can validate before computation. Adding more features by extending those
constants would make the artifact contract implicit, encourage runtime discovery,
and make it difficult to tell whether a semantic change is compatible with an old
artifact.

The v0.3 feature platform needs a small controlled registry. The registry must
describe what a feature set requires and how it becomes available without becoming
a user-authored plugin system or a database copy of feature rows. A reader must be
able to reject an unknown feature-set version before selecting data or running
calculations.

## Decision

### The registry is a reviewed, immutable contract

The repository owns one versioned registry document. A registry entry is identified
by the pair `(feature_set, feature_set_version)` and declares:

- a non-empty ordered list of output feature names and their exact value types;
- the entity type and decision frequency;
- the required canonical input dataset contracts, including the fields and
  historical-selection policy needed from each dataset;
- the required calendar and decision-clock policy;
- an explicit lookback requirement;
- a null/rejection policy;
- the computation delay and derived availability rule; and
- the generator/implementation version that produced the contract.

The registry is a closed allowlist. It is loaded from a checked-in JSON document,
validated against `feature-set-registry.schema.json`, and resolved by exact feature
set and version. Unknown names, versions, input contracts, policies, or output
types fail closed. The runtime does not import arbitrary Python modules, scan for
functions, infer schemas from annotations, or accept a caller-supplied registry
entry as a substitute for the checked-in contract.

The first registry entry re-describes `market-basic` 1.0.0. Its existing artifact
schemas remain the authoritative row and manifest contract. The registry is
therefore an admission and planning layer, not permission to reinterpret an
existing artifact. Any change to feature meaning, input selection, clock, null
policy, output type, or generator compatibility creates a new feature-set version;
old artifacts remain readable only through their original versioned contract.

### Inputs and historical selection

An input requirement names a canonical dataset and contract version, its entity
scope, required fields, and whether historical selection is required. A feature
producer receives selected, manifest-backed inputs from `ResearchCatalog`; it never
calls a provider or reads a latest-only PostgreSQL projection. The registry records
the requirement for an explicit `available_at` cutoff and an `observed_at` cutoff
when those fields exist. Receipt-time or installation-replay data may be listed only
when the entry explicitly says so; it cannot be relabeled as historically safe by a
batch invocation.

The registry's `lookback` is a semantic minimum, not an instruction to fill missing
history. A missing or ineligible prerequisite produces the declared null or reject
outcome. It must not be forward-filled, replaced by a current value, or silently
shortened without being reflected in the output coverage report.

### Clocks, delay, and nulls

Each entry names one supported decision frequency and one calendar/decision-clock
policy. For the initial `market-basic` entry the policy is the existing
`after_close_next_session` clock. The registry does not create sessions or calendar
versions; it requires the batch input to carry an exact calendar pin when the
artifact contract requires one.

The declared non-negative computation delay is applied after the latest selected
input availability:

```text
feature.available_at = max(selected input available_at) + computation_delay
```

Null policy is explicit per feature set. A missing prerequisite may result in a
typed `null` with a reason in a later coverage report, or in a rejected row/entity;
the choice is not inferred from a Python exception. Null feature values are not
labels and do not authorize future outcomes to be joined into the feature artifact.
Labels, forward returns, and any other future outcome remain separate contracts and
are outside the v0.3 registry.

### Publication and lineage

The registry itself is small checked-in metadata. Feature rows remain immutable
content-named Parquet parts and are referenced by manifests. A batch manifest must
record the exact registry name/version and a fingerprint of the registry document,
alongside its selected input manifests, calendar/universe definitions, and output
parts. Changing the registry bytes therefore creates a new reproducibility identity
and cannot mutate an existing artifact.

PostgreSQL may catalog feature artifact metadata after immutable publication, but it
does not own registry truth or feature rows. Registration/catalog writes occur only
after the filesystem publication is verified and are reconciliable if the process
dies between stores.

## Consequences

- Feature-set semantics are reviewable before a runner computes rows.
- Old artifacts can be validated against the exact contract that created them.
- A batch runner can reject unsupported versions and missing historical input policy
  before making a partial publication.
- The registry carries deliberate duplication with artifact schemas, but that
  duplication makes planning and compatibility explicit rather than implicit.
- New feature families require a reviewed registry entry, schema/fixture coverage,
  and a new version when their semantics change.

## Non-goals

This ADR does not add dataset-wide batching, feature rows in PostgreSQL, taxonomy
matching, labels, strategies, backtests, portfolios, execution, user-authored
features, or automatic feature discovery. Those boundaries require separate
contracts and acceptance evidence.
