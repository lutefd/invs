# ADR 0007: Versioned exchange calendars and decision clocks

- Status: Accepted
- Date: 2026-08-13
- Historical-artifact amendment: 2026-08-23

## Context

Weekday arithmetic is not a trading calendar. Holidays, exchange-specific closures,
early closes, daylight-saving transitions, and later calendar corrections all change
when a signal can execute. A historical query also needs to know which calendar
version was available at its decision time. Without an explicit exchange timezone
and session boundary, an after-close signal can be placed into an impossible bar.

## Decision

### Calendars are versioned data

The canonical calendar boundary consists of:

- a `calendar-manifest` that identifies the exchange MIC, IANA timezone, calendar
  version, source evidence, availability, and a fingerprint; and
- explicit `trading-session` rows for each covered local session date.

The first supported contract names XNAS, XNYS, and BVMF as MIC-scoped values, but a
calendar is not accepted merely because its MIC is known. Each source must supply
the actual session rows and a bounded coverage statement.

Each session stores its local `session_date`, UTC `open_at` and `close_at`, status,
and early-close marker. Closed dates are explicit rows in a manifest; readers never
infer a session from a weekday or manufacture a close by timezone arithmetic. An
open session requires both instants and satisfies `open_at < close_at`; a closed
session has neither instant. The exchange timezone is retained for display and
source conversion, while canonical comparisons use UTC instants.

Calendar corrections publish a new version/fingerprint or an append-only revision.
An experiment records the exact version and fingerprint it used. A historical
resolver requires that version and selects only rows with `available_at <=
decision_at`; equal-ranked conflicting rows are an error.

### Historical artifact admission

An exchange's current calendar page is eligible only from local receipt time unless
the page itself supplies an auditable historical publication boundary. Historical
admission instead requires an immutable official artifact or notice that identifies
its calendar year and publication/as-of date. Date-only publication evidence becomes
eligible at the next midnight in the publisher's local timezone.

Some official annual calendars and circulars are PDFs whose visual table/color
encoding is not a stable machine-readable data contract. A bounded declarative
transcription is permitted only when all of the following are true:

- the exact official URL and expected lowercase SHA-256 are configured;
- every covered date override, regular session boundary, MIC, timezone, and source
  locator is explicit rather than inferred from a generic holiday algorithm;
- the collector downloads and retains the artifact before publication and refuses
  any byte-hash mismatch;
- availability and revision order are explicit and validated against the artifact's
  publication/as-of statement; and
- the acceptance report independently records the artifact, hash, transcription,
  and boundary checks.

The declared mapping is therefore an auditable parser contract for one pinned
artifact, not permission to synthesize calendars from YAML or reuse the mapping for
new bytes. Weekends remain explicit compiler-produced closed rows within the bounded
coverage, as already required by this ADR.

Each correction or replacement artifact publishes a new immutable full calendar
version. It does so even when the correction affects another exchange service and
the selected equity-session fingerprint is unchanged. That distinction preserves
what source version was knowable without claiming that an irrelevant correction
changed equity trading hours.

### Decision clock

The first supported execution policy is:

```text
after_close_next_session
```

A signal whose decision timestamp is at or after an explicit session close can first
execute at the next open session in the pinned calendar. A timestamp one
microsecond before the close is still before the close. Exactly at the close is
after-close for this policy. If the decision is on an explicit closed date or
between sessions, the next eligible open session is selected. There is no implicit
same-day execution after close.

`trading_session_at` uses half-open session instants:

```text
[open_at, close_at)
```

Thus the close instant is not inside the just-ended session, and the next-session
resolver owns the transition. Decision timestamps and calendar availability use the
same inclusive `<=` boundary as the rest of the point-in-time contracts.

### Research API boundary

The first Python resolver slice exposes the narrow contract:

```text
trading_session_at(mic, calendar_version, timestamp, decision_at)
next_trading_session(mic, calendar_version, after, decision_at)
after_close_execution_session(mic, calendar_version, decision_at)
```

These functions are pure over validated session records. They do not fetch a
provider, infer a holiday, or choose a calendar version. The future PostgreSQL
publication path must preserve the same fields and add interval/uniqueness checks.

The durable repository additionally selects the latest calendar manifest for an
explicit data source and MIC whose `available_at <= decision_at`. Equal-ranked
versions with different version IDs, raw hashes, or session fingerprints fail
closed. Downstream research never asks for an unqualified "latest" calendar.

Every feature, experiment, or simulation artifact that applies a market decision
clock records a calendar pin containing:

```text
data_source_id, mic, calendar_version, session_fingerprint,
calendar_available_at, decision_clock_policy
```

The first and only accepted policy remains `after_close_next_session`. The pin is
part of the artifact input fingerprint so changing a calendar version, correction,
or clock policy changes artifact identity.

## Consequences

- Session and execution behavior is reproducible across US and Brazil timezones.
- Early-close and holiday behavior is testable at exact microsecond boundaries.
- Calendar corrections invalidate or fork an experiment through a changed
  fingerprint instead of silently changing its fills.
- A calendar source needs raw evidence, coverage, and availability semantics just
  like an observation provider.
- Static official artifacts may be transcribed only through the exact-hash,
  fail-closed bounded admission contract above.
- Calendar corrections can change source-version lineage without changing the
  selected session fingerprint; both facts remain visible.
- Research artifacts cannot be reproduced from price/macro inputs alone; they must
  also carry the selected calendar and decision-clock pin.
- Strategy and backtester implementation remains out of scope until the historical
  identity, membership, market-data, action, and FX gates pass.

## Rejected alternatives

- `weekday < 5` session inference: it misses exchange holidays and early closes.
- Storing only local wall-clock strings: it cannot reproduce daylight-saving
  conversion or compare decisions across exchanges.
- A single mutable “latest calendar”: later corrections would alter old experiments.
- Executing at the same close that produced a signal: it assumes information was
  available before the close and creates lookahead.
