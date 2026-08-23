# B3 InstrumentsConsolidated bounded security-master evidence

- Date: 2026-08-22
- Implementation: `c43204f` (`feat(data): add bounded B3 instrument source`)
- Scope: exact-ticker, current/reference B3 instrument identity and listing evidence
- Status: **admitted for the bounded snapshot role; not admitted as complete historical security-master coverage**

## Decision

The public B3 `InstrumentsConsolidatedFile` is sufficient for a bounded Brazil
identity/listing slice when the requested tickers are explicitly mapped to the
configured universe. The adapter retains the complete downloaded CSV before
parsing, filters only exact configured tickers in the `CASH` /
`EQUITY-CASH` /
`SHARES` categories, verifies the configured ISIN, and publishes one
source-backed identifier row plus one listing row per accepted instrument.

This is not a claim that the daily file is a complete historical security
master. B3's public download page advertises the listed-instrument registration
file with only a ten-day history. The source-defined `TradgStartDt` and
`TradgEndDt` fields are instrument trading interval dates, not legal incorporation
dates or proof of the original listing. The collector publishes no
`UniverseMembership` rows from this file.

## Source and access boundary

The source is B3's public listed-data download surface:

- [B3 data for download](https://www.b3.com.br/pt_br/noticias/dados-para-download.htm)
- [B3 InstrumentsConsolidated glossary](https://www.b3.com.br/data/files/79/62/B2/71/8AA6A8103234E0A8AC094EA8/Glossario%20InstrumentsConsolidatedFile%202023.pdf)
- Public table/download host: `https://arquivos.b3.com.br/tabelas/`

The adapter uses the public download request followed by the returned download
token. No B3 credentials are required for this path. The token is used only for
the transport request and is not copied into collector raw-manifest attributes.

## Live evidence

The opt-in acceptance command was:

```sh
INVS_B3_LIVE=1 \
B3_REPORT_DATE=2026-08-21 \
B3_TICKERS=PETR4,VALE3 \
go test ./internal/providers/b3 -run '^TestLiveInstrumentsConsolidated$' -count=1 -v
```

It passed against the real B3 download:

| Field | Observed value |
| --- | --- |
| Report date | `2026-08-21` |
| File | `InstrumentsConsolidatedFile_20260821_1.csv` |
| Bytes | `38,305,686` |
| Source rows read | `169,129` |
| SHA-256 | `d3cbe763d723e555bb259eac107f15e62f61fae06af5cfb4fcb50a7d3d0e93fc` |

The exact requested rows were:

| Ticker | ISIN | Company | Trading start | Distribution ID |
| --- | --- | --- | --- | --- |
| `PETR4` | `BRPETRACNPR6` | `PETROLEO BRASILEIRO S.A. PETROBRAS` | `2026-07-23` | `229` |
| `VALE3` | `BRVALEACNOR0` | `VALE S.A.` | `2026-08-12` | `223` |

The checked raw fixture is a small three-row source sample at
[`internal/providers/b3/testdata/instruments-2026-08-21.csv`](../../internal/providers/b3/testdata/instruments-2026-08-21.csv).

## Canonical mapping policy

- `TckrSymb` is retained as a BVMF-scoped ticker assertion.
- `ISIN`, `CrpnNm`, and `TradgCcy` are retained from the B3 row.
- `TradgStartDt`/`TradgEndDt` become the source-defined listing/instrument
  validity interval. An open-ended `9999-12-31` becomes `NULL`.
- `available_at` and `recorded_at` are the durable receipt time of the complete
  B3 file. The report date is not treated as a publication timestamp.
- The configured universe must provide the exact security UUID, issuer UUID,
  `ISIN`, `B3` exchange, `BVMF` MIC, and `BRL` currency. A missing or mismatched
  mapping fails closed; names and fuzzy ticker guesses are never used.
- The source has no correction revision exposed by this path. The bounded
  publication uses the report-date ordinal as a sortable snapshot revision and
  records that provisional policy in run metadata. It is not evidence of a
  vendor correction chain.

## Admission checklist

| Requirement | Result | Boundary |
| --- | --- | --- |
| Stable instrument identifier | Pass | B3 ISIN is present and exact configured mapping is required |
| Exact ticker/listing identity | Pass | Source ticker plus configured B3/BVMF mapping |
| Issuer/listing fields | Pass | Company name, currency, MIC/exchange mapping, primary-listing flag from explicit config |
| Raw retention and replay locator | Pass | Complete CSV, SHA-256, parser metadata, and row locator retained |
| Receipt-time availability | Pass | Conservative installation-knowledge timestamp |
| Historical lifecycle coverage | Fail for full gate | Public file is a short current/reference window; `TradgStartDt` is not original legal listing history |
| Universe membership history | Not provided | No membership rows are emitted |
| Correction/publication revision chain | Partial | Source distribution ID is retained; no source correction chronology is exposed |
| Corporate actions and delistings | Separate evidence slice only | See the [B3 corporate-action evidence report](2026-08-23-b3-corporate-actions-evidence.md); canonical publication remains blocked |

## Consequence

B3 now owns the bounded Brazil identity/listing ingestion path, while Yahoo
`.SA` remains a separate price bridge candidate and is not consulted for
security-master publication. The v0.2 historical-truth gate remains open until
the Brazil slice has a source-backed lifecycle/membership strategy and the
corresponding point-in-time bias audit.
