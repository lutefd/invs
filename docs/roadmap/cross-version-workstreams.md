# Cross-Version Workstreams

> Execution view of the [complete full-version roadmap](../full-version-roadmap.md).
> These controls apply to every version in the [roadmap index](README.md).


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
