# ADR 0004: Explicit failure, atomic publication, and idempotent reruns

- Status: Accepted
- Date: 2026-08-12

## Context

Collectors will be interrupted, rate limited, and rerun. Partial output must never
look complete, and retries must not duplicate observations or hide changed payloads.

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
published and no records were rejected. `partial` means useful output was published
but the requested scope was incomplete or contained rejections. `failed` means the
requested dataset version was not published. A run may retain raw artifacts in any
terminal state for diagnosis and replay.

The `run_key` describes logical work, not a process attempt (for example,
`sec-companyfacts/2026-08-12/CIK0000320193`). Claiming it uses an atomic PostgreSQL
insert or row transition. A second worker seeing the same active key does not perform
the work. A retry of a terminal failure reuses preserved raw content and creates a
new key with an explicit attempt suffix or reset operation recorded in audit metadata.

## Publication protocol

1. Insert/claim the run and set it to `running`.
2. Download to a temporary file while computing SHA-256.
3. Atomically install the content-addressed raw object. Existing identical content is
   a success; an existing different hash at a logical key is a conflict.
4. Validate and normalize to temporary Parquet files.
5. Verify schema, row counts, keys, hashes, and temporal invariants.
6. Atomically publish a new immutable dataset manifest referencing complete files.
7. In one PostgreSQL transaction, store final counters/cursor and mark the run
   `succeeded` or `partial`.

Readers discover data only through committed manifests, so abandoned temporary files
are invisible. Cleanup may remove stale temporary files after checking that no active
lease owns them.

## Retry and error policy

- Retry only transient transport, timeout, rate-limit, and 5xx failures with bounded
  exponential backoff plus jitter; honor `Retry-After`.
- Do not retry authentication, schema, semantic validation, or deterministic 4xx
  failures without a state/configuration change.
- Preserve structured error class, request/source context with secrets redacted, and
  the first/last failure. Never convert exhausted retries to empty success.
- Cancellation propagates through contexts and produces `cancelled`, preserving
  already downloaded raw objects.
- Checkpoints/cursors advance only in the same transaction that publishes the output
  they describe.

## Idempotency tests

For every adapter, tests execute the same fixture twice, interrupt once after raw
write and once before manifest publication, then rerun. Assertions cover stable row
counts, byte-identical manifests, no duplicate natural keys, preserved raw hashes,
and an explicit conflict when identical natural keys contain different data.

## Consequences

Collectors need explicit state transitions and manifests, but crashes become
recoverable and observable. Partial data cannot silently masquerade as a complete
research snapshot.
