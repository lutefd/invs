# B3 COTAHIST Brazil price bridge

- Date: 2026-08-24 (`America/Sao_Paulo`)
- Provider parser/config: `47c01de` (`feat(provider): parse B3 historical quotes`)
- Collector publication: `317be53` (`feat(collector): publish B3 historical prices`)
- Nullable publication projection: `167ce2a` (`fix(metadata): preserve unknown price publication`)
- Status: **accepted as the bounded v0.2 Brazil price bridge for installation replay**

## Decision

The Brazil price bridge uses B3's official COTAHIST archive instead of Yahoo `.SA`.
The admitted path accepts only closed annual files and an explicit bounded date
range. Every configured ticker must map to exactly one BR/B3/BVMF/BRL security and
an exact B3 ISIN; the provider never discovers identity from a symbol or company
name.

The parser validates the ZIP member, 245-byte header/data/trailer layout, BOVESPA
source identity, trailer count, round-lot BDI code `02`, cash market `010`, BRL
reference currency, quotation factor, exact ticker/ISIN, OHLC invariants, and record
uniqueness. It publishes manifest-backed daily prices with `price_basis=raw`, date
observation precision, an absent `published_at`, and local durable receipt as
`available_at`.

That availability rule is deliberate. The annual archive and layout establish the
trading date and values but do not provide an exact historical publication instant
for each row. These prices are therefore reproducible and safe for installation
replay after receipt, but they are not backdated into an earlier decision. The
research catalog returns no COTAHIST rows for a decision before receipt.

## Official source boundary

- [B3 historical quotations page](https://www.b3.com.br/pt_br/market-data-e-indices/servicos-de-dados/market-data/historico/mercado-a-vista/cotacoes-historicas/)
- [B3 COTAHIST fixed-width layout](https://www.b3.com.br/data/files/65/50/AD/26/29C8B51095EE46B5790D8AA8/HistoricalQuotations_B3.pdf)
- Accepted annual archive: `https://bvmf.bmfbovespa.com.br/InstDados/SerHist/COTAHIST_A2021.ZIP`

B3 describes the archive as historical quotations since 1986 and states that the
prices are supplied in the currency and quotation form of the time without
inflation or corporate-action adjustment. The layout marks itself public
information and defines the annual file, 245-byte records, cash-market code, OHLC,
trade quantity, quotation factor, and ISIN fields.

This project retains the source only for the user's private research installation.
The acceptance does not authorize redistribution or a public derived-data service.
Yahoo remains unadmitted for raw-first unattended retention; it is not used by this
bridge.

## Retained evidence

The clean acceptance root is
`/home/luis/invs-acceptance/2026-08-24-b3-cotahist-final`; the isolated database is
`invs_v02_b3_price_final_20260824`. The collector was built from full commit
`167ce2ac1268af27aaf7e7cc3cad328bfacbfb57` as
`invs-collector@sha256:99d4b7bd53899b0cde7e62429c081f2931168dfd9dca4599a8792cfd6bbc0bb7`.

| Evidence | SHA-256 |
| --- | --- |
| Official 2021 COTAHIST ZIP | `b0d4aed17d609c7ba615a23320c2fd513b580a251ae7d884a5015ef8a3fe4705` |
| Raw run manifest | `f1028ba9f32d4e8f21482f69c28609aedd392c9ef6f6a1cf72d273b50c8edd0e` |
| Canonical price manifest | `1918551d234211961057b2faea865259ac8c3853a93d24a86db848d9abc5c77b` |
| Canonical price Parquet | `6e96503eed29320e8a7d2a68b3f6ae400dcce1e62f3a01a7bfa79f0b8f7938f0` |

The manifest records schema `1.0.0`, normalizer `b3-cotahist-v1`, exact build
commit, data-source ID `7ad7113e-5a6d-4f85-8c73-cd75bc09c50a`, ingestion-run ID
`47ba3248-5db2-4b94-8f92-096d4e96c309`, and four rows for security
`fcb3f84d-e8e8-46ad-aace-70027962523f` (PETZ3, ISIN `BRPETZACNOR2`).

PostgreSQL records run key
`acceptance-b3-cotahist-petz3-final-2021-09-06-10/b3_cotahist` as `succeeded`:
1,831,862 source data records scanned, four rows written, zero rejected, and one raw
object. Repeating the exact key skipped without fetching or writing again.

The earlier root `/home/luis/invs-acceptance/2026-08-24-b3-cotahist` is preserved.
Its first run exposed the old snapshot table's non-null publication assumption and
was explicitly cancelled with reason `snapshot projection migration required`.
Migration `000009_nullable_price_publication` now preserves a null source
publication in the operational projection; its fresh-image, rollback, and reapply
paths passed the PostgreSQL harness.

## Published slice and decision boundary

| Trading date | Open | High | Low | Close | Quantity |
| --- | ---: | ---: | ---: | ---: | ---: |
| 2021-09-06 | 26.60 | 27.14 | 26.24 | 26.61 | 3,649,900 |
| 2021-09-08 | 26.42 | 26.49 | 25.40 | 25.57 | 4,027,800 |
| 2021-09-09 | 25.45 | 27.65 | 25.30 | 26.98 | 4,354,600 |
| 2021-09-10 | 27.10 | 27.10 | 26.06 | 26.45 | 4,637,400 |

All four rows have raw basis, date observation precision, no publication timestamp,
and exact availability `2026-08-24T05:01:54.445298Z`. `point_in_time_inputs`
returned zero rows at `2026-08-24T05:01:54.445297Z` and all four exactly one
microsecond later at availability. Each row retains its ZIP, member, line, date,
ticker, ISIN, source hash, and manifest/part lineage.

## Validation

The accepted boundary passed all Go tests and vet, 19 JSON Schemas, 75 Python tests,
Ruff, the PostgreSQL migration/replay harness, a clean live collection, exact-key
skip, content-addressed Parquet readback, and exact before/at availability checks.

## Remaining boundary

The price bridge closes the last source-publication unit before the v0.2 exit audit.
The final bounded US/Brazil audit must classify this dataset as installation-replay
only and prove that it cannot leak into an earlier historical decision. No strategy,
backtester, portfolio, or execution behavior is admitted here.
