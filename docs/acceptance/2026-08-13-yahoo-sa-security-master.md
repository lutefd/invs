# Yahoo `.SA` Security-Master Source Admission

- Date: 2026-08-13
- Scope: bounded live verification of Yahoo Finance `.SA` quote/chart responses
- Status: **not admitted as historical security-master evidence**
- Retained role: candidate market-data bridge only, subject to a separate terms and
  unattended-access review

## Decision

Yahoo `.SA` responses are sufficient to confirm that a configured vendor symbol
currently resolves to a São Paulo equity quote and to collect a price series. They
are not sufficient to publish a source-backed `SecurityIdentifierVersion`,
`SecurityListingVersion`, or `UniverseMembership` into the historical-truth boundary.

The response path does not provide the stable issuer/security identity, exchange MIC,
primary-listing assertion, historical validity interval, universe membership event,
revision chain, or conservative historical availability timestamp required by the
repository contracts. The Yahoo terms reviewed for this check also require a
separate permission decision before unattended automated collection and prohibit
redistribution of Finance information.

This result does not change the existing Yahoo daily-price adapter into a historical
security-master adapter. It explicitly prevents the current Yahoo symbol mapping from
being mistaken for historical identity evidence.

## Live evidence

The live checks ran at `2026-08-13T20:10:32Z` with these bounded requests:

- Yahoo chart response for `PETR4.SA` and `VALE3.SA`, daily interval, a bounded
  2000-01-01 through 2025-12-31 range;
- Yahoo quote-search response for the same current symbols; and
- the same chart/search checks for `LAME4.SA` and `BRDT3.SA` as current-quote absence
  probes, without inferring a delisting event from absence.

The two current symbols returned:

| Symbol | Chart | Search metadata | Yahoo fields observed | Identity fields absent |
| --- | --- | --- | --- | --- |
| `PETR4.SA` | 6,528 daily points | current quote | `symbol=PETR4.SA`, `currency=BRL`, `exchangeName=SAO`, `fullExchangeName=São Paulo`, `instrumentType=EQUITY`, display name | ISIN, UUID, issuer identifier, MIC, listing-validity dates, membership history |
| `VALE3.SA` | 6,528 daily points | current quote | `symbol=VALE3.SA`, `currency=BRL`, `exchangeName=SAO`, `fullExchangeName=São Paulo`, `instrumentType=EQUITY`, display name | ISIN, UUID, issuer identifier, MIC, listing-validity dates, membership history |

Both chart responses exposed `firstTradeDate=2000-01-03T12:00:00Z`. The field has no
source-defined listing-effective or public-availability meaning in the response, and
the identical coarse value for these two securities is not evidence of their legal
listing dates. It must not be used as `valid_from` or `available_at`.

The current-search response exposed a display name and exchange label, but its quote
record had no ISIN or UUID. The chart response likewise exposed no issuer identity,
MIC, primary-listing flag, membership event, revision number, or historical
announcement/publication timestamp. `LAME4.SA` and `BRDT3.SA` produced no current
search quote and a chart 404; that is only evidence of current Yahoo coverage for
those requests, not a historical removal event.

The official B3 company page independently identifies Petrobras' `PETR4` listing and
ISIN `BRPETRACNPR6`, which illustrates the missing cross-source security identifier
that Yahoo did not return in this check:
[B3 Petrobras listed-company record](https://sistemaswebb3-listados.b3.com.br/listedCompaniesPage/main/9512/PETR/corporate-actions?language=en-US).

## Admission checklist

| Requirement | Yahoo `.SA` result | Decision |
| --- | --- | --- |
| Stable security or issuer identity | Symbol and display name only; no stable ID/ISIN in the checked metadata | Fail |
| Unambiguous listing identity | São Paulo label and `SAO`; no MIC or source-backed primary-listing assertion | Fail |
| Historical identifier/listing intervals | No `valid_from`, `valid_until`, rename, re-listing, or correction events | Fail |
| Universe membership history | No benchmark/curated-universe membership resource or event history | Fail |
| Revision behavior | No source revision/version chain for metadata | Fail |
| Availability semantics | Receipt time is known; historical public availability is not | Fail |
| Historical price coverage | Daily chart data exists for the checked current symbols | Pass for price-bridge investigation only |
| Terms and unattended access | Yahoo documents `.SA` coverage and a 15-minute delay, but also says Finance data must not be redistributed; Yahoo terms prohibit automated collection without express prior permission | Requires permission review |

Yahoo's coverage and provider disclosures are documented in [Exchanges and data
providers on Yahoo Finance](https://help.yahoo.com/kb/account/exchanges-data-providers-yahoo-finance-sln2310.html).
The general [Yahoo Terms of Service](https://legal.yahoo.com/us/en/yahoo/terms/otos/index.html)
prohibit automated collection without express prior permission and restrict creating
databases or competing aggregated data sources from Yahoo material. The historical
download help also says availability depends on data licensing restrictions:
[Download historical data in Yahoo Finance](https://help.yahoo.com/kb/finance/certain-amounts-sln2311.html).

## Consequence for this repository

- Do not create historical-truth rows from Yahoo display names, `.SA` suffixes,
  `SAO`, or `firstTradeDate` alone.
- Do not infer `BVMF` from the Yahoo exchange label without an explicit mapping source.
- Do not treat a successful current quote as proof that the security belonged to a
  historical universe or was knowable at a historical decision time.
- Do not retain or publish Yahoo metadata fixtures until the terms/permission review
  authorizes the intended raw-first retention model.
- The durable publication/resolution boundary from `d963180` remains tested but empty
  for live security-master rows.

The next source-admission slice should use an authoritative B3 instrument/listing
source for Brazil and an explicit SEC/benchmark source for the bounded US slice. It
must still satisfy the repository's source checklist: natural keys, historical
coverage, revision behavior, availability semantics, raw-retention policy, fixtures,
and live acceptance. Yahoo can be revisited as a separate price bridge after the
terms and price-history policy are resolved.
