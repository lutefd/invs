# B3 UP2DATA corporate-action lifecycle evidence

- Date: 2026-08-23
- Implementation: `8b9916f` (`feat(provider): parse B3 UP2DATA corporate-action lifecycle`)
- Scope: public B3 UP2DATA sample layout and a transport-agnostic lifecycle parser
- Status: **sample-contract evidence accepted; production UP2DATA access and canonical publication remain gated**

## Decision

B3 documents a stronger corporate-action product than the public listed-company
display used by the first evidence slice. Its Corporate Action channel has
Schedule, Life Cycle, Corporate Action, and Issuer folders. The documented Life
Cycle file consolidates daily schedule information with active confirmed events;
the Corporate Action folder includes next-day events, updates, and cancellations.

The public sample is sufficient to add a bounded parser for
`CorporateActionLifeCycleFileV2`. The parser retains the exact CSV bytes and
typed source-native rows with:

- `CorpActnCtrlNb` as the source event/control identifier;
- `RptDt` and `PblctnDt` as source dates;
- ticker, B3 security ID, market identifier, and product/request/result ISINs;
- source event type/description and `EvtActnTpCd` action state;
- correction indicator/reason fields;
- reference, ex, payment, subscription, trading, and correction dates; and
- decimal/value lexemes without binary floating-point conversion.

This is not canonical `CorporateAction` publication. `PblctnDt` is date-only,
the product's delivery/availability semantics are not yet part of the installed
collector, and the source action/correction codes still require a documented
mapping and revision policy. No UP2DATA credentials, transport, or paid-product
dependency was added.

## Official source boundary

- [B3 UP2DATA available data](https://b3.com.br/en_us/market-data-and-indices/data-services/up2data/available-data/)
- [B3 UP2DATA corporate-action sample ZIP](https://b3.com.br/data/files/CE/F5/F6/71/2643881036DB3088AC094EA8/Eventos%20Corporativos-Corporate%20Action.zip)
- [B3 UP2DATA how to hire and access](https://b3.com.br/en_us/market-data-and-indices/data-services/up2data/how-to-hire-and-access/)
- [B3 UP2DATA FAQ](https://b3.com.br/en_us/market-data-and-indices/data-services/up2data/faq/)

B3's access documentation treats UP2DATA as a contracted product. It describes
channel selection, an access kit/installation keys or cloud credentials, and
certificates after the hiring process. The repository therefore treats the
downloadable sample as layout evidence only, not as authorization to build an
unattended production collector.

## Live sample verification

The opt-in acceptance command was:

```sh
INVS_B3_UP2DATA_LIVE=1 \
go test ./internal/providers/b3 \
  -run '^TestLiveUP2DataCorporateActionLifecycleSample$' -count=1 -v
```

It fetched the official sample ZIP and parsed the member
`Corporate_Action/LifeCycle/Corporate_Action_CorporateActionLifeCycleFileV2_20230419_1.csv`:

| Field | Observed value |
| --- | --- |
| Sample ZIP bytes | `8,020,893` |
| Sample ZIP SHA-256 | `b23299941df165d860b1289edc3fdfebb2b90772ea430160f37085586030a053` |
| Lifecycle member bytes | `3,428,342` |
| Lifecycle member SHA-256 | `63c4fa6816f462d03e1bccc53c6d52f8de7eb3ffceb15b472b5012450c933c48` |
| Data rows received | `10,312` |
| Typed events retained | `10,283` |
| Exact duplicate rows skipped | `26` |
| Rows rejected | `3` |
| First source event ID | `1E12917D1E504313951A81787F2272A3` |
| First report date | `2022-12-14` |

The three rejected rows contain unescaped semicolons inside the free-text
meeting-update field; the parser fails those rows closed while retaining the
complete file. A few rows have only extra trailing separators and are accepted
after dropping those empty columns. This behavior is deliberate: no shifted
source fields are guessed.

## Adapter and test boundary

- `internal/providers/b3/up2data_corporate_actions.go` parses one lifecycle CSV
  member without assuming a network or product transport.
- `internal/providers/b3/up2data_corporate_actions_test.go` covers exact raw
  retention, event identity, dates, decimal lexemes, malformed-row handling,
  and the opt-in live public sample check.
- `internal/providers/b3/testdata/up2data-corporate-action-lifecycle-v2.csv`
  is a small fixture derived from the documented sample shape.

The parser returns a source-native result only. It does not write metadata,
create canonical IDs, publish a `published_at` instant, or apply price
adjustments.

## Admission checklist

| Requirement | Result | Boundary |
| --- | --- | --- |
| Stable event/control identifier | Pass at sample-layout level | `CorpActnCtrlNb` is retained and validated |
| Security linkage | Partial | Ticker/B3 ID/ISIN fields are retained when present; issuer-level rows still need an explicit policy |
| Source publication evidence | Partial | `PblctnDt` is present in the lifecycle sample but date-only |
| Update/correction state | Partial | Action/correction fields are retained; revision ordering and cancellation semantics remain to be mapped |
| Effective/payment dates | Pass where supplied | Source date lexemes are parsed as UTC civil dates |
| Raw retention/replay | Pass | Exact CSV bytes, SHA-256, member identity, and row locators are retained |
| Authorized production access | Not admitted | UP2DATA access requires product-specific contracting/access material |
| Canonical `CorporateAction` publication | Blocked | Requires availability, timestamp precision, action mapping, and revision policy |

## Consequence and next gate

The B3 corporate-action gate is narrower but materially improved: the repository
now knows the official product layout that can support event identity and
correction-aware ingestion, while continuing to fail closed on the public web
display and on malformed sample rows.

The next implementation gate is authorized access to the Corporate Action
channel (or a public versioned alternative), followed by a product transport
adapter that captures file delivery metadata and daily lifecycle history. Until
that gate passes, the listed-company endpoint remains receipt-time source-native
evidence only and no canonical price-adjustment dataset is published.
