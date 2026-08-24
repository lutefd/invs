# SEC filing metadata publication boundary

- Date: 2026-08-24 (`America/Sao_Paulo`)
- Provider normalization: `a6d3c17` (`feat(provider): normalize SEC filing metadata`)
- Collector publication: `84c09f5` (`feat(collector): publish SEC filing metadata`)
- Nested primary documents: `f07d3a9` (`fix(provider): admit SEC nested primary documents`)
- Source date semantics: `39db6d0` (`fix(provider): preserve SEC source date semantics`)
- Growing-container replay: `5347462` (`fix(data): preserve SEC filing identity across snapshots`)
- Status: **accepted for the bounded v0.2 SEC filing-metadata gate**

## Decision

The SEC submissions response now produces canonical filing metadata separately from
company facts. Each row retains the accession number as its source-document
identity, form, filing date, optional source `reportDate`, exact EDGAR acceptance
timestamp, primary-document path, archive URL, issuer identity, raw-record locator,
and complete manifest-backed lineage.

The exact SEC acceptance timestamp is both `published_at` and `available_at`.
`filing_date` and `reportDate` remain distinct source civil dates and never override
that knowledge boundary. In particular, `reportDate` is retained as `period_end`
without being promoted to `observed_at`: proxy statements can name a future meeting
date, and using that date as an observation cutoff would hide an already-public
filing. The parser also accepts safe nested primary-document paths while rejecting
absolute paths, traversal, queries, and fragments.

SEC submissions are a growing response container. An unchanged accession remains
idempotent when a later submissions response has a different whole-payload hash;
the first raw lineage is preserved. A canonical metadata change under the same
accession still fails with a natural-key conflict. CVM's version-sensitive replay
rule remains unchanged.

The submissions feed does not expose an exact amended-accession relationship for
these rows. `amends_source_document_id` therefore remains empty rather than being
inferred from form names or filing order. Full filing documents and narrative
extraction remain outside this v0.2 boundary.

## Retained evidence

The clean retained acceptance directory is
`/home/luis/invs-acceptance/2026-08-24-sec-filings-final`; its isolated PostgreSQL
database is `invs_v02_sec_filing_final_20260824`. The final collector was built from
full commit `53474628083913d9822283d2f407a674762408a6` as
`invs-collector@sha256:8e91de2fbd1ab43d8fc718e28c97e52dd6dbe3255e0f37be987b2ba11a908e14`.

| Evidence | SHA-256 |
| --- | --- |
| SEC submissions response | `53db7f6600c8fd2e5393c2a153a85d893bc71fc00d6b9b2ceaaf637871be52cc` |
| Raw run manifest | `b4b28a0ddcd88c181b5a46e8d93ab99aac9d2dc0dd03cf5fc0404dd71bceefe4` |
| Canonical filing manifest | `015cbd5dc3b2d8a4ef6a9d8677cdcd5314149c59b960592d2547147501e5f4ff` |
| Canonical filing Parquet | `e91788d69fa1afdfcf2396b725586fa9c95571f6939d7054b2af47b2e95182a0` |

The canonical manifest records schema `1.0.0`, normalizer
`sec-submissions-v1`, 1,001 rows, data-source ID
`8ccda4d9-37f5-4ca6-ae44-a379069f0fdc`, and ingestion-run ID
`4746e218-9c99-400d-90d6-ca4117ff77f6`.

PostgreSQL records run key
`acceptance-sec-aapl-filings-final-2026-08-24/sec` as `succeeded`, with 26,136
records received and written, zero rejected, and two raw objects. The output is
25,135 canonical fundamentals plus 1,001 canonical filings. Repeating the exact run
key returned the existing terminal run without another fetch or write.

An earlier retained directory,
`/home/luis/invs-acceptance/2026-08-24-sec-filings`, contains the development runs
that exposed nested-path and source-date edge cases. It was not rewritten or used as
the final acceptance root.

## Exact decision checks

The latest retained AAPL filing is accession `0001140361-26-033928`, form `4`, with
primary document `xslF345X06/form4.xml` and archive URL
`https://www.sec.gov/Archives/edgar/data/320193/000114036126033928/xslF345X06/form4.xml`.
Its exact publication and availability are `2026-08-20T22:30:16Z`.

`filings_as_of` excluded that accession at
`2026-08-20T22:30:15.999999Z` and included it exactly at
`2026-08-20T22:30:16Z`. The retained proxy accession
`0001308179-26-000008` is available at `2026-01-08T21:31:36Z` even though its
source `reportDate` is the later `2026-02-24`; `observed_at` is absent. All 1,001 SEC
rows have an absent `observed_at`, so selection is controlled by exact acceptance.

The source also contains 36 filings whose civil `filing_date` is the next date after
the UTC acceptance date. For example, accession `0000320193-24-000081` was accepted
at `2024-08-01T22:03:34Z` and has filing date `2024-08-02`. Both values are retained
without rejecting the row or changing availability.

## Validation

The boundary passed:

```sh
make test
docker compose build collector
docker compose --profile collect run --rm --no-deps ... collector --source sec ...
python -c 'ResearchCatalog("/data").register(); ...'
```

The final suite contained all Go tests and vet, 19 JSON Schemas, 75 Python tests,
and Ruff. Live verification independently reopened the content-addressed Parquet
through `ResearchCatalog`, checked exact before/at availability, a future proxy
report date, a next-civil-day filing date, and a safe nested primary document.

## Remaining boundary

The next v0.2 unit is the Brazil price bridge, followed by the combined bounded US
and Brazil point-in-time bias audits. No strategy, backtest, or execution work is
admitted by this acceptance.
