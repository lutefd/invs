# ADR 0006: Source-backed historical identity and universe membership

- Status: Accepted
- Date: 2026-08-13

## Context

The configured YAML universe and the existing `security_identifiers` catalog are
useful for current collection, but they cannot answer which security a ticker
identified on an earlier date. A renamed or delisted security must remain
addressable, and a backtest must not silently replace its historical universe with
today's survivors. Exchange scope also matters: the same ticker can identify
different securities on different markets.

The platform needs two temporal questions for identity and membership:

1. What was valid in the market or universe at `as_of`?
2. Which version of that fact was knowable by `decision_at`?

Those questions must remain separate from `recorded_at`, which only describes when
this installation stored the evidence.

## Decision

### Source-backed records are a separate historical contract

Historical identity is published through source-backed, append-only records rather
than inferred from the current YAML mapping. The v0.2 canonical contracts are:

- `security-identifier-version`: a scoped identifier assignment such as ticker,
  vendor symbol, or exchange code;
- `security-listing-version`: the security's issuer relationship, exchange/MIC,
  currency, and primary-listing state over a validity interval; and
- `universe-membership`: a versioned assertion that a security is or is not in a
  named research universe over a validity interval.

Each record carries a stable record ID, source reference, data-source ID, raw
payload hash, `available_at`, and `recorded_at`. `available_at` is the conservative
knowledge cutoff used by historical research. It is not inferred from a ticker's
effective date or from the local receipt time.

The current YAML mapping remains a current-configuration input. Historical APIs
must fail closed when no eligible source-backed record exists; they must not fall
back to YAML or a current-universe lookup.

### Intervals and identity scope

All validity intervals are half-open:

```text
[valid_from, valid_until)
```

`valid_until = null` means the source-backed assertion has no known exclusive end,
not that the record is valid for every future date. Ticker lookup requires the
identifier type, normalized value, and explicit scope. A MIC is the default scope
for exchange tickers; `global` is permitted only for an identifier system that is
globally unique.

Renames are represented by ending one interval and starting another. Delistings
end the listing and identifier intervals; the security UUID and prior records are
retained. Re-listings use a new listing interval and do not reuse a current ticker
mapping as historical evidence. Issuer-to-security relationships are carried by
the listing version so an issuer change is also an explicit historical fact.

### Revisions and deterministic selection

Source corrections are append-only. A later revision may overlap an earlier
assertion in market time because the two rows describe different knowledge vintages;
it never overwrites the earlier row. For a requested `as_of` and `decision_at`, the
resolver:

1. keeps rows with `available_at <= decision_at`;
2. groups revisions of the same source assertion (same source, security, scoped
   identity or listing key, and `valid_from`) and selects the latest knowable
   correction;
3. keeps those selected corrections whose validity interval contains `as_of`; and
4. selects the latest eligible assertion by the documented total order
   `(available_at, revision, recorded_at, record_id)`.

Correction collapse precedes the validity check. This is required when a later
source notice shortens an earlier open-ended interval: once that correction is
knowable, the superseded open-ended row cannot continue leaking beyond the corrected
`valid_until`. Equal-ranked corrections that disagree about interval or identity
fail closed.

An equal-ranked set that resolves to different securities, listing facts, or
membership states is an ambiguity error. It is never resolved with fuzzy name or
ticker matching. If multiple authorities are admitted later, their source
priority must be explicit in the contract and included in that total order before
the source is used in a historical audit.

An authoritative source may not publish overlapping intervals for the same
identifier scope/value or the same listing key. Database exclusion constraints and
fixture tests must enforce that invariant. Revision overlap is allowed only when
the revision/availability order makes the selected vintage unambiguous.

### Universe membership is not a current security filter

Membership rows include an explicit `member` state so a later removal or correction
can be represented without deleting the earlier assertion. `universe_as_of` applies
the same validity and knowledge cutoffs independently for each security. A security
that was historically a member remains eligible for that historical query even if
it is delisted or absent from the current configured universe.

The first bounded fixtures cover one synthetic US benchmark and one synthetic Brazil
benchmark. They deliberately include a same-ticker/different-MIC case, a rename,
a delisting, and a membership removal that is unavailable until a later decision.
They are contract tests, not live-market coverage.

### Research API boundary

The intended APIs are explicit about both clocks:

```text
security_identifier_as_of(identifier_type, value, scope, as_of, decision_at)
listing_as_of(security_id, mic, as_of, decision_at)
universe_as_of(universe_id, as_of, decision_at)
```

The first Python resolver slice implements these semantics over validated records.
PostgreSQL publication, interval constraints, and collector integration are the
next implementation slice and remain unaccepted until their migration and live
evidence exist.

## Consequences

- Historical research can distinguish market validity from knowledge availability.
- Ticker changes, duplicate tickers, delistings, and membership removals remain
  reproducible without mutating old records.
- Every backtest must carry an explicit universe and decision cutoff; “current
  universe” is a dashboard convenience, not a historical input.
- The platform carries a small amount of duplicate-looking metadata while the
  current catalog and the source-backed historical publication boundary coexist.
- A source admission still requires coverage, terms, provenance, revision, and
  fixture evidence before it can support a v0.2 bias audit.

## Rejected alternatives

- Treating YAML as a time series: it has no source-backed validity or availability
  history and would create silent survivorship and ticker-change bias.
- Using ticker as a security primary key: tickers are scoped, reusable, and mutable.
- Overwriting the current identifier row: it destroys the evidence needed to replay
  an earlier decision.
- Choosing the first matching source or fuzzy company name: that hides ambiguity
  and makes the answer provider-order dependent.
