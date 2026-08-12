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

A simulation has one monotonically increasing `decision_at` instant. A record is
eligible only when all of the following hold:

```text
published_at is known
published_at <= decision_at
ingested_at is ignored for historical knowledge, except in live replay
the selected vintage is the latest version published on or before decision_at
```

In a live/paper replay, information also requires `ingested_at <= decision_at` so the
simulation matches what this installation actually possessed. `observed_at <=
decision_at` is necessary for measurements, but never sufficient.

Unknown `published_at` means unavailable to a point-in-time backtest unless a
source-specific, documented conservative availability policy supplies it. It must
not default to `observed_at`, `period_end`, or `ingested_at`.

Trading signals computed after a market close may first trade at the next executable
session unless the strategy and input publication times prove an earlier execution
was possible. Calendar, timezone, and execution-delay policies are inputs to the
backtest and are stored with its result.

## No-lookahead invariants

- Dataset readers require an explicit `decision_at` (or an explicit opt-out named
  `latest`, which is forbidden in backtests).
- Adjusted prices must identify their adjustment method and knowledge cutoff.
  Future splits/dividends may not alter bars visible to an earlier decision.
- Universe membership and identifier resolution use versions valid at `decision_at`,
  not today's ticker or constituents.
- Fundamental and economic queries select vintages by `published_at`, including
  restatements and ALFRED-style revisions.
- Derived features record the maximum publication timestamp of every input. Their
  `available_at` is at least that maximum plus the declared computation delay.
- Tests use adversarial fixtures where a later revision differs materially from its
  first release.

## Consequences

Queries are slightly more verbose and require source-specific time parsing, but the
platform can explain exactly why a value was visible to a strategy. “Latest” views
remain useful for dashboards but are deliberately separated from research views.
