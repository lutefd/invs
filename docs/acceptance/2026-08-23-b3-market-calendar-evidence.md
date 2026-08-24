# B3 market-calendar evidence

- Date: 2026-08-23
- Implementation: `6f61a84` (`feat(provider): parse B3 market-calendar evidence`)
- Scope: source-native listed-market closure and special-hours notices for an explicit B3 calendar year
- Status: **calendar evidence accepted; canonical trading-session publication remains gated**

## Decision

B3 publishes a multi-year market-calendar page with date-specific notices for
listed, OTC, clearing, depositary, and other segments. The bounded adapter
selects an explicit year, retains the complete HTML response, and accepts only
rows whose description explicitly identifies the listed B3 market. It records:

- the exchange-local calendar date;
- B3's event label;
- `closed`, `special_hours`, or `listed_notice` status;
- the source description and deterministic raw row locator; and
- the exact raw page hash and receipt time.

It does not synthesize open/close instants or publish `TradingSession` rows. The
same page mixes market segments, and B3's regular and special trading hours are
defined separately by the official trading-hours material.

## Official source boundary

- [B3 market calendar](https://b3.com.br/en_us/solutions/platforms/puma-trading-system/for-members-and-traders/trading-calendar/holidays/)
- [B3 2026 calendar announcement](https://www.b3.com.br/pt_br/noticias/calendario-de-negociacao-da-b3-confira-o-funcionamento-da-bolsa-em-2026.htm)
- [B3 trading hours](https://www.b3.com.br/main.jsp?doui_processActionId=setLocaleProcessAction&locale=pt_BR&lumA=1&lumII=8A80CB81633FBF0B0163402B3B335F48&lumPageId=8A6882694E91F2D4014E9248DBB001D4)

The calendar announcement identifies the 2026 closure dates and the special
Ash Wednesday schedule. The separate trading-hours page supplies the regular
market schedule. These are related official sources, but they are not yet one
versioned machine-readable calendar artifact in the repository.

## Live verification

The opt-in acceptance command was:

```sh
INVS_B3_CALENDAR_LIVE=1 \
B3_CALENDAR_YEAR=2026 \
go test ./internal/providers/b3 \
  -run '^TestLiveB3MarketCalendar$' -count=1 -v
```

It fetched the official English calendar page and parsed the explicit 2026
section:

| Field | Observed value |
| --- | --- |
| Raw HTML bytes | `328,003` |
| Raw HTML SHA-256 | `0cc3d5decf0d684263e16717612e0445835465511c4fe92048b1b55f877c99bc` |
| Calendar rows received | `24` |
| Listed-market events retained | `15` |
| Listed-market closures | `14` |
| Special-hours notices | `1` |
| Rejected rows | `0` |

The 14 closures include the listed-market dates published for New Year's Day,
Carnival, Good Friday, Tiradentes, Labor Day, Corpus Christi, Independence Day,
Our Lady of Aparecida, All Souls' Day, Zumbi and Black Awareness Day, Christmas
Eve, Christmas Day, and New Year's Eve. The one special-hours event is Ash
Wednesday; it is retained as a special notice rather than mislabeled as an
early close.

## Adapter and test boundary

- `internal/providers/b3/market_calendar.go` owns the explicit-year request,
  raw resource retention, narrowly scoped HTML extraction, and source-native
  event typing.
- `internal/providers/b3/market_calendar_test.go` covers raw retention,
  listed-market filtering, closure/special-hours classification, malformed
  source boundaries, and the opt-in live check.
- `internal/providers/b3/testdata/market-calendar-2026.html` is a small fixture
  for the official page shape.

The parser deliberately skips rows that only describe FX, OTC, clearing, or
other non-listed segments. It does not infer a closure from an unrelated
segment notice.

## Admission checklist

| Requirement | Result | Boundary |
| --- | --- | --- |
| Explicit calendar year | Pass | The request must name the year |
| Listed-market scope | Pass at source-evidence level | Rows require `Listed B3` or `B3 Listed` evidence |
| Closure dates | Pass at source-evidence level | `no trading on the equity` notices become `closed` |
| Special-hours notices | Pass at source-evidence level | Ash Wednesday is retained as `special_hours` |
| Regular open/close instants | Not admitted | Must be joined to the separate B3 trading-hours source |
| Weekend/session policy | Not admitted | The page is a notice page, not a complete session table |
| Publication/revision chronology | Partial | Raw receipt is retained; page publication/version semantics are not exposed |
| Canonical `TradingSession` publication | Blocked | Requires hours, weekend policy, availability, and revision policy |

## Consequence and next gate

The B3 calendar work is now at the same honest evidence boundary as the B3
corporate-action work: the official source is captured and parsed, but the
canonical historical layer remains protected from guessed session times.

The next calendar gate is to capture the official regular-hours artifact and
date-specific special-hours artifact together, define the BVMF
`America/Sao_Paulo` session semantics, and publish a complete year only after
the source availability and revision policy is explicit.
