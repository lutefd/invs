# ADR 0002: Point-in-time timestamps and no-lookahead rules

- Status: Accepted
- Date: 2026-08-12

## Context

Financial facts describe both the world and the history of what market participants
could know. Period ends, public release times, effective dates, revisions, and local
receipt times are different facts. Conflating them introduces lookahead bias.

## Decision

Canonical records use UTC instants encoded as RFC 3339 strings. PostgreSQL uses
`timestamptz`. Source-local timestamps are converted with an explicit IANA timezone;
the original source value is retained in the raw artifact. A date-only source value
must remain a date or carry documented market-calendar resolution; it must never be
silently interpreted as midnight UTC.

Normalized Parquet stores canonical instants in UTC physical timestamp fields with
microsecond precision. Any input instant with a non-zero sub-microsecond nanosecond
remainder is rejected before conversion; it is never silently truncated. Exact
microsecond values round-trip unchanged. Date-only fields such as `period_start` and
`period_end` remain day fields and are not subject to this instant-precision rule.

The temporal fields have non-overlapping meanings:

- `observed_at`: when a measurement applies to the world. For a daily bar this is
  the interval close; for an economic value it is the observation/reference date;
  for a fundamental it is normally the fiscal period end. It is not availability.
- `published_at`: earliest defensible instant the exact version became public. SEC
  acceptance time and an agency release time are examples. If a source exposes only
  a date, the normalizer records the precision and uses a conservative availability
  policy defined for that source.
- `effective_at`: instant an event takes legal or economic effect, such as a split,
  ticker change, merger, or dividend ex-date. It is not its announcement time.
- `ingested_at`: when this system durably received the raw bytes. This is lineage and
  operational latency, not historical public availability.
- `period_start` / `period_end`: interval covered by a flow or report. They do not
  replace either `observed_at` or `published_at`.

For records that are revised, each published version is append-only. The natural key
includes its source and revision/vintage identity; corrections never overwrite an
older vintage.

## Availability rule

A simulation has one monotonically increasing `decision_at` instant. This research
slice uses the normalized `available_at` field as its conservative knowledge cutoff.
The field is computed or supplied by the source normalizer under a documented
availability policy. A record is eligible only when all of the following hold:

```text
available_at is known
available_at <= decision_at
observed_at <= decision_at
published_at is retained source metadata, not the research cutoff
ingested_at is ignored for historical knowledge, except in live replay
the selected vintage is the latest eligible version under the query's total order
```

In a live/paper replay, information also requires `ingested_at <= decision_at` so the
simulation matches what this installation actually possessed. `observed_at <=
decision_at` is necessary for measurements, but never sufficient without
`available_at <= decision_at`.

Unknown `published_at` does not become a historical cutoff by inference. The source
normalizer must instead supply an explicit conservative `available_at`; missing
`available_at` means unavailable to a point-in-time backtest. It must not default to
`observed_at`, `period_end`, or `ingested_at`.

Trading signals computed after a market close may first trade at the next executable
session unless the strategy and input publication times prove an earlier execution
was possible. Calendar, timezone, and execution-delay policies are inputs to the
backtest and are stored with its result.

## No-lookahead invariants

- Dataset readers require an explicit `decision_at` (or an explicit opt-out named
  `latest`, which is forbidden in backtests).
- Adjusted prices must identify their adjustment method and knowledge cutoff.
  Future splits/dividends may not alter bars visible to an earlier decision.
- The current YAML security-to-issuer mappings are configuration inputs only; this
  slice does not pretend they provide historical identifier resolution.
- Fundamental and economic queries select eligible vintages by `available_at`, with
  `observed_at` also gated by `decision_at`; `published_at` remains source metadata.
- Derived features record the maximum input knowledge cutoff. Their `available_at` is
  at least that maximum plus the declared computation delay.
- Tests use adversarial fixtures where a later revision differs materially from its
  first release.

## Consequences

Queries are slightly more verbose and require source-specific time parsing, but the
platform can explain exactly why a value was visible to a strategy. “Latest” views
remain useful for dashboards but are deliberately separated from research views.
