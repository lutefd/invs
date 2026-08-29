# ADR 0011: Feature artifact catalog registration

- Status: Accepted
- Date: 2026-08-29

## Context

The v0.3 batch runner publishes an immutable dataset-level manifest and immutable
child feature artifacts under the feature root. Those manifests are authoritative for
feature values, but filesystem discovery alone does not provide an operational index
for finding a batch, inspecting its point-in-time inputs, or tracing its accepted
partitions back to the requested universe and decision schedule.

PostgreSQL already owns small, transactional metadata and operational state. It must
not become a feature-row store or a substitute for the manifest contract. There is
also no cross-store distributed transaction: a process can stop after filesystem
publication and before metadata registration, so registration must be independently
verifiable and safe to retry.

## Decision

### Register only after immutable publication

The recommended operator path validates the checked-in feature-set registry, the
dataset-level batch manifest, every listed child manifest, and every listed output
part before writing PostgreSQL metadata. Paths are stored relative to the configured
feature root, and the manifest/part hashes remain the authoritative object identity.
An invalid, unsupported, or tampered batch fails closed and creates no catalog row.

The registration boundary is dataset-level. It records the batch identity and
version, feature-set identity, registry/generator/Git fingerprints, decision range,
calendar pin and clock policy, universe and input fingerprints, input fitness labels,
output manifest hash, partition counts, and publication status. Feature values are
read from the referenced Parquet child artifacts, not copied into PostgreSQL.

### Store explicit lineage in normalized tables

The catalog uses one parent `feature_artifacts` row and separate child tables for:

- requested decision points;
- ordered universe members;
- selected canonical input manifests and parts;
- historical-fitness and availability labels for each input dataset; and
- accepted child feature partitions, including their manifest/part paths, hashes,
  row counts, security IDs, and decision timestamps.

The complete normalized registration envelope, including these lineage slices, is
hashed as `registration_sha256`. The database preserves the parent and all lineage
rows in one transaction, with foreign keys and uniqueness constraints protecting the
relationship. The catalog is metadata for discovery, lineage, and operator queries;
it is not authoritative history and it does not replace the feature manifest.

### Retries are idempotent and conflicts fail closed

`artifact_id` is the batch identity. Re-registering the same complete normalized
envelope returns success with `already_present`; reusing the identity for different
metadata or lineage returns a conflict without changing the existing row. The
feature-set/version plus input fingerprint also has a uniqueness boundary so a
different batch cannot silently publish the same logical input selection as another
artifact.

The catalog currently accepts the published status only. `invalid` and `orphaned`
are reserved metadata states for later reconciliation work; registration does not
invent them when publication or validation fails.

## Consequences

- Operators can discover validated feature batches and inspect their explicit lineage
  without scanning arbitrary files or loading feature rows into PostgreSQL.
- A catalog row can be reconciled against the immutable feature root by verifying its
  recorded manifest and part hashes.
- The registration path duplicates selected manifest metadata, but the duplication is
  deliberate: it makes discovery and historical-fitness review transactional while
  preserving Parquet as the feature-value boundary.
- A future coverage/reporting reader can build on the stored decision, universe,
  fitness, and partition rows without changing the feature-value contract.

## Non-goals

This ADR does not add recursive feature discovery, a feature-store service, row-level
feature storage in PostgreSQL, automatic orphan repair, coverage dashboards, new
feature families, labels, strategies, backtests, portfolios, execution, or ML.
