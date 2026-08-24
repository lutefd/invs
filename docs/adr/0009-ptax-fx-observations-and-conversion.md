# ADR 0009: PTAX FX observations and conversion policy

- Status: Accepted
- Date: 2026-08-24

## Context

The v0.2 historical-truth gate needs an explicit USD/BRL input for comparing and
valuing the bounded US and Brazil slices. The existing BCB SGS adapter publishes
generic economic observations and conservatively uses local receipt time because an
SGS daily row does not expose its historical bulletin timestamp. Relabeling that
current download as historical FX would weaken the point-in-time boundary.

The Banco Central do Brasil publishes a dedicated PTAX OData dataset. Its closing
dollar endpoint returns `cotacaoCompra`, `cotacaoVenda`, and `dataHoraCotacao` for
each daily closing bulletin. BCB describes PTAX as the daily reference rate for real
against US dollars, formed from four dealer consultations and published in a fifth
bulletin. The official dataset has daily dollar history from 28 November 1984 and is
licensed under ODbL.

Primary references:

- [BCB PTAX methodology study](https://www.bcb.gov.br/conteudo/relatorioinflacao/EstudosEspeciais/EE042_A_taxa_de_cambio_de_referencia_Ptax.pdf)
- [Circular 3.506](https://normativos.bcb.gov.br/Lists/Normativos/Attachments/49545/Circ_3506_v1_O.pdf)
- [BCB daily exchange-rate dataset](https://dadosabertos.bcb.gov.br/pt_BR/dataset/taxas-de-cambio-todos-os-boletins-diarios)
- [BCB PTAX OData service](https://olinda.bcb.gov.br/olinda/servico/PTAX/versao/v1/odata/)

## Decision

### Source and observation contract

PTAX is a separate disabled-by-default provider and canonical dataset named
`bcb_ptax`; it is not another configured BCB SGS macro series. Collection uses only
the official `CotacaoDolarPeriodo` endpoint with explicit inclusive start and end
dates and these selected fields:

```text
cotacaoCompra, cotacaoVenda, dataHoraCotacao
```

The first admitted pair is:

```text
base_currency  = USD
quote_currency = BRL
rate_unit      = BRL per USD
rate_kind      = ptax_closing
```

One canonical `FXObservation` 1.0.0 record preserves both buy and sell rates. It
also records `fixing_at`, `fixing_timezone=America/Sao_Paulo`,
`published_at`, `available_at`, `revision`, `source_record_id`, `recorded_at`, and
complete raw provenance. Decimal values are exact nonnegative strings and both rates
must be positive with `buy_rate <= sell_rate`.

`dataHoraCotacao` is the source timestamp of the daily closing bulletin. It is parsed
as Sao Paulo civil time using the historical IANA timezone and preserved at the
source's microsecond precision. For the admitted closing record:

```text
fixing_at    = source bulletin timestamp
published_at = source bulletin timestamp
available_at = source bulletin timestamp
recorded_at  = durable local receipt time
```

The BCB methodology describes the daily PTAX as published in the fifth bulletin,
and the official OData record supplies that bulletin timestamp. A record is therefore
historically eligible at the exact returned timestamp, not at local installation
receipt. Rows dated after receipt fail. Special-day bulletin times remain
source-declared; the collector never assumes a constant 13:10 clock.

The natural key is:

```text
(source, base_currency, quote_currency, rate_kind, fixing_at, revision)
```

The first source version is revision 0. The endpoint does not expose correction IDs
or revision chronology. An exact repeat is idempotent. A different value at the same
source key is retained in raw storage but blocks canonical publication; it is not
silently assigned a guessed historical revision. A future source-admission change
must define correction availability before advancing the revision.

Canonical history is manifest-backed Parquet under an FX partition. PostgreSQL may
carry operational latest projections or catalogs later, but does not become a
duplicate bulk FX time-series store.

### Conversion policy

The first and only admitted policy is:

```text
ptax_sale_direct_inverse_1_0_0
```

It selects one exact `bcb_ptax` closing observation with
`available_at <= decision_at`. Selection is explicit by source, pair, and requested
fixing date; absence of that date fails. The policy does not silently carry a prior
business-day fixing forward.

For an amount `A` and PTAX sell rate `R`, quoted as BRL per USD:

```text
USD -> BRL: A * R
BRL -> USD: A / R
```

Same-currency amounts are identity conversions and use no FX observation. Every
other pair is unsupported. Policy 1.0.0 performs no triangulation, midpoint
construction, buy/sell switching, or caller-selected inversion. Supporting another
currency requires a separately admitted direct observation or a new versioned
triangulation policy.

Arithmetic uses exact rational values. Terminating base-10 results are emitted
exactly. Non-terminating results use 50 significant decimal digits with
round-half-even, matching the corporate-action adjustment boundary.

Every non-identity conversion returns an input pin containing:

```text
observation id, source, base/quote currencies, rate kind, revision,
fixing_at, available_at, sell_rate, canonical record hash,
raw payload hash, ingestion run id, and policy version
```

Later valuation artifacts must embed that complete pin. A converted number without
the pin is not a reproducible research input.

## Consequences

- USD and BRL valuation has one explicit orientation and rate side.
- The source bulletin timestamp closes the historical-availability gap left by SGS.
- Special-day publication clocks come from the retained source row.
- Research fails on missing dates instead of inventing FX sessions or staleness.
- Inverse conversion is reproducible and cannot be confused with a differently
  quoted pair.
- Broader currencies and triangulation remain intentionally outside v0.2.

## Rejected alternatives

- Treating SGS series 1 or 10813 receipt time as historical availability: that only
  supports installation replay.
- Storing only an unlabeled number such as `5.2236`: orientation and rate side would
  be ambiguous.
- Using a midpoint: the roadmap requires a canonical rule, and BCB operational
  guidance uses PTAX sale for foreign-asset conversion into reais.
- Allowing callers to choose multiply or divide: pair orientation owns that choice.
- Carrying the last rate across missing dates: doing so requires an explicit calendar
  and staleness policy not admitted in version 1.0.0.
- Triangulating through USD by default: a hidden multi-hop policy would introduce
  unpinned inputs and mismatched timestamps.
