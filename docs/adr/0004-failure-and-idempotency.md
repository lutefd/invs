# ADR 0004: Explicit failure, atomic publication, and idempotent reruns

- Status: Accepted
- Date: 2026-08-12

## Context

Collectors will be interrupted, rate limited, and rerun. Partial output must never
look complete, retries must not duplicate observations or hide changed payloads, and
latest-only operational snapshots must not advance from unvalidated or stale candidates.

## Decision

Every run has a caller-supplied `run_key` unique within a data source and follows:

```text
queued -> running -> succeeded
                  -> partial
                  -> failed
                  -> cancelled
```

Terminal runs have `finished_at`; non-terminal runs do not. Counters are monotonic and
non-negative. `succeeded` means all accepted output and manifests were durably
published and no records were rejected. `partial` means the requested scope was
incomplete, contained rejections, or encountered an error; it may still publish
accepted output or raw evidence, and successful entities in a partial run may publish
latest-only snapshots. `failed` means the requested normalized dataset version was not
published. A run may retain raw artifacts in any terminal state for diagnosis and
replay.

The `run_key` describes logical work, not a process attempt (for example,
`sec-companyfacts/2026-08-12/CIK0000320193`). Claiming it uses an atomic PostgreSQL
insert or row transition. A second worker seeing the same active key does not perform
the work. A retry of a terminal failure uses a new key with an explicit attempt suffix
or follows the archive/reset policy; preserved raw content may be reused by hash, but
conflicting payloads are never overwritten.

## Publication protocol

1. Insert/claim the run and set it to `running`.
2. Download to a temporary file while computing SHA-256.
3. Atomically install the content-addressed raw object. Existing identical content is
   a success; an existing different hash at a logical key is a conflict.
4. Parse and validate the provider response. If parsing fails after raw bytes were
   returned, preserve those bytes and record the error; do not emit normalized rows or
   snapshot candidates from that response.
5. Normalize accepted records to a temporary Parquet file.
6. Verify schema, row counts, keys, hashes, temporal invariants, and raw provenance.
7. Rename the file to its immutable content-named `part-<sha256>.parquet`, sync it, and
   atomically publish `manifest.json` referencing the complete part set.
8. Before finalization SQL, validate every snapshot candidate's run lineage and reduce
   the batch to one winning price per security or macro observation per series. In one
   PostgreSQL transaction, upsert those accepted latest-only projections, store final
   counters/cursor, and mark the run `succeeded`, `partial`, or another terminal state.

Readers discover data only through committed `manifest.json` files. They verify the
manifest and read only its content-named immutable parts; `data.parquet`, unlisted
parts, and recursive Parquet globs are never canonical input. Abandoned temporary or
unlisted files are therefore invisible to research readers.

## Retry and error policy

- Retry only transient transport, timeout, rate-limit, and 5xx failures with bounded
  exponential backoff plus jitter; honor `Retry-After`.
- Do not retry authentication, schema, semantic validation, or deterministic 4xx
  failures without a state/configuration change.
- Preserve structured error class, request/source context with secrets redacted, and
  the first/last failure. Never convert exhausted retries to empty success.
- If a provider parse/schema error includes a response body, persist that raw object
  before finalization. The run remains non-success and publishes no accepted row or
  snapshot for the invalid response; a stored raw object makes the outcome partial
  when it is the only progress.
- Context cancellation that reaches finalization produces `cancelled`, preserving
  already downloaded raw objects. A process that dies before finalization may leave a
  queued/running orphan active run; it is not auto-cancelled. Only an operator may
  cancel such a run, with a non-empty reason, and operator cancellation changes only
  the active run state/error metadata—it never publishes or deletes snapshot data.
- Checkpoints/cursors advance only in the same transaction that publishes the output
  they describe.

## Idempotency tests

For every adapter, tests execute the same fixture twice, interrupt once after raw
write and once before manifest publication, then rerun. Assertions cover stable row
counts, byte-identical manifests, no duplicate natural keys, preserved raw hashes,
and an explicit conflict when identical natural keys contain different data. Large
historical macro fixtures must reduce to one finalization candidate per series before
the PostgreSQL transaction, while lineage validation still examines every candidate.
Parse-error fixtures must preserve the returned raw bytes and publish no normalized
manifest or snapshot for the invalid response. Orphan recovery must require explicit
operator intent.

## Consequences

Collectors need explicit state transitions, manifests, and latest-only projection
rules, but crashes become recoverable and observable. Partial data cannot silently
masquerade as a complete research snapshot, and replaceable operational snapshots
cannot become a second history store.
