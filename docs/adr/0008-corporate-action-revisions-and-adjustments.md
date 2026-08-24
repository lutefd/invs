# ADR 0008: Corporate-action revisions and adjustment artifacts

- Status: Accepted
- Date: 2026-08-24

## Context

Raw vendor-adjusted prices cannot explain which split or dividend version they used,
when that version became knowable, or whether a later correction changed history.
The existing `CorporateAction` 1.0 schema distinguishes publication and effect but
does not carry the availability, precision, revision, or state fields required by
the v0.2 point-in-time boundary. The existing B3 adapters also establish two
different source qualities:

- the public listed-company endpoint has values and dates but no stable event ID,
  historical publication time, or correction sequence; and
- the public UP2DATA sample has event IDs, source publication/report dates, ex-dates,
  values, and update/correction fields, but not admitted product-delivery history.

SEC EDGAR provides a stronger bounded US path. An immutable accepted filing has a
unique accession, exact acceptance timestamp, and stable document locators. An
issuer exhibit can therefore establish the announcement, terms, and first adjusted
trading date for an action without treating a current vendor table as historical
truth.

## Decision

### Canonical action versions

Canonical corporate actions advance to schema 2.0.0. Required action-version fields
are:

```text
id, security_id, source_event_id, revision, action_status, action_type,
observed_at, observed_precision, published_at, published_precision,
available_at, effective_at, effective_precision,
source_reference, recorded_at, provenance
```

Optional economic fields remain explicit: `record_date`, `payment_date`, ratio
numerator/denominator, cash amount/currency, and target security. Decimal values are
canonical nonnegative strings; split ratios additionally require both sides to be
strictly positive.

The natural key is `(data_source_id, source_event_id, revision)`. The event ID names
one source event family; revisions are append-only versions within that family.
`action_status` is one of `active`, `cancelled`, or `unsupported`. A correction never
overwrites an earlier row. At a decision time, the resolver first chooses only rows
whose `available_at <= decision_at`, then selects the highest
`(available_at, revision, recorded_at, id)` rank for each event family. Equal-ranked
different records fail closed. A selected cancellation removes the action from the
active result, while a selected unsupported state remains a blocking result.

`observed_at` is the source-declared ex-date or first split-adjusted trading date.
The first adjustment policy accepts date precision only and compares the civil UTC
date encoded at midnight; it does not infer an ex-date from settlement rules or a
weekday. `effective_at` is the legal/economic effect when the source supplies it.
When only the explicit first adjusted date exists, the same date-precision value is
used for both fields and that limitation remains visible in the precision marker.

### Availability by source

SEC action evidence is historically eligible at the EDGAR acceptance timestamp,
converted from the documented Eastern Time acceptance clock to UTC. The canonical
`source_event_id` is the immutable accession plus document locator and action name,
for example:

```text
0000320193-20-000060/exhibit-99.1/four-for-one-split
```

An amended filing has its own accession in `source_reference` but must explicitly
reuse the original action's `source_event_id` and advance its revision. The exact
filing index and action document are retained before publication; configured hashes
and declarative field locators must match.

The public B3 UP2DATA sample is admissible only for installation replay. Its
date-only `PblctnDt` remains source metadata, but `available_at` is the retained raw
receipt time because the sample does not prove when historical product files were
delivered to subscribers. Multiple rows for one `CorpActnCtrlNb` remain append-only
revisions in source report order. Unknown action-state codes become `unsupported`,
not active guesses. Authorized production delivery metadata or another admitted
source is required before a B3 sample row can be visible to an earlier historical
decision.

The listed-company endpoint remains raw evidence only. A deterministic local row
locator is not a substitute for a source event family and revision chain.

### Supported adjustment policy

The first and only accepted adjustment policy is:

```text
backward_split_dividend_1_0_0
```

It supports `split`, `reverse_split`, and `cash_dividend`. Every other selected
action type, unsupported state, missing economic field, nonpositive factor, or
currency mismatch blocks publication for the affected price interval.

For a split ratio `post_shares : pre_shares`, bars strictly before the source
`observed_at` date use:

```text
price_factor  = pre_shares / post_shares
volume_factor = post_shares / pre_shares
```

For a cash dividend amount `D`, let `P` be the last raw close strictly before the
source ex-date from the same pinned raw-price input manifest. The backward price
factor is:

```text
price_factor = (P - D) / P
```

The policy rejects `P <= 0`, `D >= P`, absent prior closes, and currency mismatch.
Cash dividends do not change volume. Actions are applied in descending observed-date
order with exact rational arithmetic; canonical output decimals never pass through
binary floating point. Terminating base-10 results are emitted exactly.
Non-terminating rational results are rounded to 50 significant decimal digits with
round-half-even, making repeating dividend factors portable and byte-reproducible.

An action contributes only when both `available_at <= decision_at` and its effect is
no later than the decision. A future or not-yet-knowable action cannot change bars in
an earlier artifact. This makes two artifacts with different decision times
legitimately different even when they read the same raw price part.

### Adjustment artifact

Adjusted prices live in a separate immutable artifact tree. Raw price Parquet parts
are never overwritten or relabeled. Each adjustment manifest records:

```text
schema_version, artifact_id, policy_version, security_id, decision_at,
raw_price_manifest_path, raw_price_manifest_sha256,
raw_price_part_sha256, raw_price_basis,
corporate_action_snapshot_sha256,
selected action ids, revisions, canonical record hashes, observation/effect times,
and availability times,
output part path, output part sha256, row count, and created_at
```

`raw_price_basis` must be `raw`. The action snapshot is the canonical ordered
selection envelope, including blocking actions; its hash and the price-manifest hash
contribute to the deterministic artifact ID. Replaying the same inputs produces the
same ID and bytes. An existing different artifact under that ID is a conflict.

The artifact writer validates all selected actions and complete input coverage
before writing. Unsupported actions produce a diagnostic and no output artifact;
there is no partially adjusted dataset. Research readers follow the manifest and
verify every hash rather than recursively discovering files.

## Consequences

- Historical US actions can be admitted from exact SEC filing evidence with an
  exact knowledge cutoff.
- The public B3 sample can exercise revision and adjustment mechanics without being
  mislabeled as historically delivered data.
- A Brazil backtest decision before installed B3 evidence is explicitly blocked,
  satisfying the roadmap's unsupported-action boundary without weakening it.
- Split and dividend arithmetic is reproducible from exact price and action inputs.
- Vendor-adjusted price series remain separate convenience inputs and cannot satisfy
  this policy.
- Mergers, spin-offs, stock dividends, rights, ticker changes, delistings, and
  exchange changes may be stored canonically but block adjustment until a versioned
  policy is accepted for them.

## Rejected alternatives

- Using a vendor's adjusted close without an action manifest: the factors and
  knowledge cutoff are opaque.
- Treating B3 `PblctnDt` in a public sample as historical delivery time: the sample
  does not establish subscriber availability.
- Deriving an ex-date from a record date and weekdays: settlement rules and exchange
  calendars are versioned inputs, not assumptions.
- Applying every action known today to every past decision: it leaks future actions
  and corrections backward.
- Publishing around unsupported actions: a plausible but incomplete return series
  is worse than an explicit blocked interval.
