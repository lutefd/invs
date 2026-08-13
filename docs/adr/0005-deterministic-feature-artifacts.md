# ADR 0005: Deterministic feature artifacts and manifests

- Status: Accepted
- Date: 2026-08-12

## Context

The normalized price-bar history is an immutable, manifest-addressed input, but a
derived feature needs its own reproducibility and knowledge boundary. A feature
value without the exact selected input parts, the as-of cutoff, and the computation
delay can be accidentally reused as if it had been available earlier or had been
computed from a different vintage.

The first feature contract is deliberately small. It establishes the artifact
boundary and a bounded engine for one security and decision timestamp without adding
collector behavior, dataset-wide orchestration, a strategy API, a backtest, or a
machine-learning pipeline.

## Decision

The schemas `feature-observation.schema.json` and `feature-manifest.schema.json`
define the `market-basic` feature set at version `1.0.0`. The feature registry is
closed and consists of these exact decimal-string-or-null fields:

- `close`: the selected daily price-bar close;
- `return_1d`: simple close-to-close return, `close_t / close_(t-1) - 1`;
- `range_1d`: daily high minus daily low in the selected price basis; and
- `volume`: the selected daily volume, or null when the source does not provide it.

The feature observation is one security row and carries an explicit `decision_at`,
the `input_available_at` cutoff of its selected inputs, the non-negative integer
`computation_delay_seconds`, the resulting `available_at`, an `input_fingerprint`,
and immutable artifact metadata. Every member of `features` is required. A feature
whose prerequisites are not available is represented by null; the feature column is
not omitted, renamed, coerced to a JSON number, or filled from a later vintage.

The feature manifest is the only committed reader pointer for an artifact. It
contains the fixed feature-set/version identity, immutable artifact metadata,
the same point-in-time timing fields, selected input manifest paths and SHA-256
hashes, selected input part paths and SHA-256 hashes, and content-named output parts
with their hashes and row counts. A changed input selection or output is a new
artifact publication; an existing artifact ID/version is never rewritten.

### Point-in-time and availability rules

`decision_at` is a required historical knowledge cutoff. It is not inferred from a
bar date, `observed_at`, `published_at`, wall-clock time, or the output's
`available_at`. The selected input rows must satisfy `available_at <= decision_at`,
and `input_available_at` is the maximum of those input availability timestamps.

The exact derived availability rule is:

```text
available_at = input_available_at + computation_delay_seconds
```

The JSON Schema validates the fields and their types; timestamp arithmetic and the
ordering rule are semantic validation responsibilities of the engine and readers. A
feature may therefore become available after the `decision_at` used to
select its inputs. A later consumer decision must still require
`feature.available_at <= consumer_decision_at`; no consumer may substitute
`decision_at`, the trading date, or `published_at`.

### Input fingerprint

`input_fingerprint` is the lowercase SHA-256 of the UTF-8 RFC 8785 canonical JSON
of this envelope:

```json
{
  "decision_at": "<UTC instant>",
  "feature_set": "market-basic",
  "feature_set_version": "1.0.0",
  "selected_input_manifests": [
    {"path": "<relative manifest path>", "sha256": "<64 lowercase hex>"}
  ],
  "selected_input_parts": [
    {"path": "part-<sha256>.parquet", "sha256": "<64 lowercase hex>"}
  ]
}
```

The two arrays are sorted by `(path, sha256)` before canonicalization. The listed
manifest and part hashes are the authoritative selection; recursive discovery,
unlisted parts, latest-only snapshots, or a provider request made at feature-build
time are not equivalent inputs. Semantic validation also checks that each
content-named part embeds the same hash recorded in its object and that the
manifest row count equals the sum of its output part row counts.

### Version and rejection policy

Both schemas pin their schema/manifest version and the `market-basic` feature-set
version. Objects reject unknown properties. The observation's `features` object and
the manifest's `feature_names` are closed to the four names above. An unknown
schema version, manifest version, feature-set version, or feature name is an
unsupported contract and must fail closed; it must not be ignored, downgraded,
forward-filled, or interpreted as a compatible version.

Exact financial values use the shared canonical decimal grammar as JSON strings.
JSON numbers, scientific notation, locale decimal separators, and binary floating
point rounding are not part of this contract.

## Non-goals

The implementation is limited to point-in-time input selection, exact-decimal
calculation, and immutable publication for the closed `market-basic` registry, one
security per call. It does not add collection, dataset-wide scheduling or discovery,
a strategy or signal API, portfolio construction, execution, a backtest result,
performance metrics, labels, training data, model artifacts, or any ML behavior.
Calendar/execution policy and any future feature set must be introduced by a separate
versioned contract.

## Consequences

Feature artifacts are explainable and replayable from immutable normalized parts,
and consumers have a concrete knowledge cutoff to enforce. The contract requires a
small amount of repeated metadata on each observation and semantic checks that JSON
Schema cannot express, but it prevents silent lookahead and schema drift at the
publication boundary.
