# ADR 0015: Theme evidence and append-only hypothesis ledger

- Status: Accepted
- Date: 2026-08-29

## Context

The feature platform produces reproducible observations, but it does not yet
preserve the reasoning that connects those observations to a falsifiable
investment view. A v0.4 research slice needs to retain a reviewed theme,
source-backed document evidence, structured event proposals, a dated thesis,
and predictions before their outcomes are known.

The reasoning layer must not become a second time-series store or a mutable
knowledge graph. It also must not promote an LLM extraction into canonical
fact without a human review boundary. Every historical research reconstruction
must be able to answer what was knowable at the decision timestamp.

## Decision

### PostgreSQL owns transactional research metadata

PostgreSQL stores stable identities and small, queryable metadata for:

- theme nodes and typed research entities;
- versioned theme memberships and typed relationship revisions;
- document metadata and references to raw/text artifacts;
- event proposal revisions and human review decisions;
- hypothesis identities, revisions, evidence links, predictions, and outcomes;
- evidence-pack registrations.

The model is relational. A theme hierarchy uses a parent foreign key and
relationships use explicit typed endpoints; no graph database is introduced.
The accepted relationship vocabulary is:

```text
SUPPLIES CUSTOMER_OF DEPENDS_ON BENEFITS_FROM EXPOSED_TO
COMPETES_WITH CONSUMES PRODUCES INDICATOR_FOR
```

Facts and interpretations are labelled separately. A relationship or
membership revision records confidence, direction, evidence references,
author/method, validity interval, recorded time, and review state. Revisions
are inserted, never edited in place. Database triggers reject updates and
deletes on append-only tables and enforce monotonically increasing revision
numbers.

### Filesystem artifacts remain the evidence boundary

Document raw bytes are retained independently from document metadata and from
deterministic extracted-text artifacts. Each text artifact records its input
raw hash, extractor/version/configuration, output hash, locators, and errors.
Parser failure leaves the raw bytes and publishes a failed text result rather
than deleting or replacing the source.

Evidence packs and research memos are self-contained, content-addressed JSON
artifacts. Their references carry hashes and availability timestamps. A pack
builder rejects any item whose availability is after the decision timestamp;
it never resolves an unqualified latest record. Markdown is a presentation
of the same JSON memo and can be re-imported only after its referenced IDs and
pack hash verify.

### LLM output is proposal-only

The first event vocabulary is deliberately narrow: capex guidance, production
guidance, material customer/supplier, financing, and theme exposure. An event
proposal stores the source document hash and exact source spans plus prompt,
model, parameters, extraction-code version, and confidence metadata. It starts
as `proposed`; a human review creates an append-only `accepted` or `rejected`
revision. The canonical observation/event tables are not written by the model.

### Predictions are frozen before measurement

Hypothesis revisions capture thesis, causal model, horizon, benchmark, bounded
universe, invalidation conditions, decision timestamp, and evidence-pack
identity. Predictions reference a specific hypothesis revision. Freezing a
prediction is a one-way database transition; any later correction requires a
new prediction identity or hypothesis revision. Outcomes append a versioned
measurement policy, realized and benchmark results, drawdown where applicable,
measurement timestamp, and pinned input artifact references. An unavailable or
invalid measurement is explicit and is never converted into a recommendation.

## Consequences

- A v0.4 thesis can be reconstructed from a dated pack without relying on
  current theme or document state.
- Reviewable extraction is useful without making probabilistic text generation
  part of canonical truth.
- The relational model remains compatible with the existing Go/pgx metadata
  boundary and avoids duplicating Parquet observations in PostgreSQL.
- Append-only revisions make corrections auditable but require callers to
  query the explicit latest accepted revision rather than mutate a row.
- The first outcome loop measures a bounded research universe only; portfolio,
  backtest, and execution semantics remain v0.5 and later boundaries.

## Rejected alternatives

- A graph database: the initial workload is small relational metadata and needs
  the same transactional store as existing catalog and identity records.
- Storing document text only in PostgreSQL: it would couple large evidence
  bytes to transactional metadata and weaken raw-artifact replay.
- Letting an LLM write canonical events: source spans and model versions do
  not replace review authorization or deterministic fact publication.
- Updating frozen predictions in place: it would make prospective evaluation
  impossible to audit.
