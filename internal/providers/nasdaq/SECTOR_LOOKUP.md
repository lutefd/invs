# Nasdaq-100 sector lookup

`nasdaq100_sector_lookup.csv` is a checked-in current-universe reference table.
Each row binds a Nasdaq-100 ticker and company name to the source's top-level
ICB Industry classification and the equivalent sector slug used by INVS. The stock manager requires both the ticker
and a normalized company-name match before applying a row. New constituents that
are absent from this file stay `unclassified` until the lookup is reviewed and
updated.

## Sources and scope

- Ticker and company-name keys are aligned to the current Nasdaq-100 constituent
  snapshot captured on 2026-09-23. The Nasdaq website's live refresh feed did
  not include usable sector values in that snapshot.
- ICB Industry labels were taken from the per-company table in the
  [Nasdaq-100 constituent list](https://en.wikipedia.org/wiki/List_of_NASDAQ-100_companies),
  which identifies the classification as of 2026-07-01. This is a secondary
  reference and should be checked when the index roster changes.
- Nasdaq's [official ICB sector overview](https://www.nasdaq.com/articles/assets-and-liquidity-nasdaq-100-ecosystem)
  confirms that the Nasdaq-100 uses Industry Classification Benchmark (ICB)
  sector classifications.

These are Nasdaq ICB labels, which can differ from GICS or other familiar sector
labels. For example, this ICB table places Netflix and Walmart in Consumer
Discretionary and PayPal in Industrials. INVS maps `Telecommunications` to
`communication_services` and `Basic Materials`
to `materials`; other ICB sector names map to lowercase underscore slugs.
Existing manual profile sectors take precedence over this lookup. A successful
lookup is recorded in profile provenance as `nasdaq_sector_lookup` and is an
inferred current classification, not a point-in-time historical fact.

Review this table when the Nasdaq-100 membership changes. Confirm both the
ticker and company name, update the ICB sector from a current reference, then run
`go test ./internal/providers/nasdaq ./cmd/stock-manager` before publishing the
change.
