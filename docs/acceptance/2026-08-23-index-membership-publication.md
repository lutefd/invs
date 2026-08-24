# Historical index-membership publication boundary

- Date: 2026-08-23 (`America/Sao_Paulo`; live receipts are 2026-08-24 UTC)
- Provider implementation: `629e82b` (`feat(provider): parse official index membership notices`)
- Publication implementation: `376d0e0` (`feat(data): publish historical universe memberships`)
- Mixed-universe identity fix: `9f488a9` (`fix(metadata): support issuers without SEC identifiers`)
- Scope: one added-then-removed security in the Nasdaq-100 and Ibovespa
- Status: **bounded US/Brazil historical membership accepted; the later combined
  identity/listing report closes the publication item**

## Decision

The collector now retains official index-change notices and publishes their exact
add/remove sequence as append-only `universe_memberships`. Publication is limited to
explicitly configured universes and ticker mappings. It fails closed on an unknown
or ambiguous ticker, a removal-only chain, repeated state, non-chronological events,
missing raw lineage, and overlapping authoritative revisions.

The accepted slice proves historical membership selection for one US benchmark and
one Brazilian benchmark. It does not establish a complete constituent history and
does not close the combined identifier/listing/membership roadmap item. Historical
identifier assignments, primary-listing intervals, and lifecycle events still need
their own admitted evidence.

## Official source boundary

### United States

- [Nasdaq-100 annual changes published 2025-12-12](https://www.globenewswire.com/news-release/2025/12/13/3204942/6948/en/annual-changes-to-the-nasdaq-100-index.html)
- [Nasdaq-100 June 2026 quarterly changes](https://www.globenewswire.com/news-release/2026/06/12/3310860/0/en/nasdaq-100-index-june-2026-quarterly-changes.html)
- [SEC filing identity for Insmed, CIK 0001104506](https://www.sec.gov/Archives/edgar/data/1104506/000110450626000009/0001104506-26-000009-index.html)

Nasdaq authored both releases and distributed them through GlobeNewswire. Their
publication timestamps include an Eastern Time clock, so `available_at` is the exact
release instant. The effective instant is the 09:30 `America/New_York` market open
on the stated effective date.

### Brazil

- [B3 September 2021 Ibovespa portfolio](https://www.b3.com.br/pt_br/noticias/b3-divulga-nova-carteira-de-ibovespa-b3-e-demais-indices.htm)
- [B3 September 2025 Ibovespa portfolio](https://www.b3.com.br/pt_br/noticias/ibovespa-8AA8D0CD9851B974019905C93D246CB2.htm)
- [CVM open-company register](https://dados.cvm.gov.br/dados/CIA_ABERTA/CAD/DADOS/cad_cia_aberta.csv)

The B3 pages expose a publication date but no publication clock. The conservative
policy makes each notice eligible only at the next `America/Sao_Paulo` midnight.
The 2026-08-24 CVM register identified Pet Center Comércio e Participações S.A. as
CVM code `25089`; its raw CSV SHA-256 was
`f8fffbc973d2ffdeb97602c4fd764bb0cc3fab3b4a3de4b3dddf284a3826a89f`.

## Exact acceptance configuration

The temporary non-secret YAML had SHA-256
`65412c0c6f2168e5497069b933cd560bd188a072c900bac778c85293b335a975` and was
removed after acceptance. Its effective mappings were:

| Source | Universe | Security | Stable security ID | Market identity |
| --- | --- | --- | --- | --- |
| Nasdaq | `nasdaq_100` | INSM | `c16dc49f-7817-41e9-8101-f0a118d7103d` | US / XNAS / USD / CIK 0001104506 |
| B3 | `ibovespa` | PETZ3 | `fcb3f84d-e8e8-46ad-aace-70027962523f` | BR / BVMF / BRL / CVM 25089 / no SEC CIK |

Only `nasdaq_membership` and `b3_membership` were enabled. The configured notice
lists were exactly the four URLs above. The collector image was built from full Git
SHA `9f488a92a0f2e961a1467567233e3b13a3366370`; its local image ID was
`sha256:1dad9d3adc7c12b8ca0657b1a777093aca977885284ea7be376e6e3e5bb3d4b6`.

## Live publication evidence

| Source | Run ID | Stable run key | Received | Published | Rejected | Raw objects |
| --- | --- | --- | ---: | ---: | ---: | ---: |
| Nasdaq | `52b14f75-5023-48b9-80a3-f5d9b1487d6e` | `membership-us-acceptance-20260824-v1/nasdaq-membership` | 22 | 2 | 0 | 2 |
| B3 | `bc48cb37-b507-4d3c-9ab1-15228ba760a4` | `membership-br-acceptance-20260824-v1/b3-membership` | 11 | 2 | 0 | 2 |

Both runs finished `succeeded`. Repeating each exact run key returned its existing
terminal run and performed no new publication.

| Evidence | Raw SHA-256 | Bytes |
| --- | --- | ---: |
| Nasdaq 2025 annual changes | `d7e51501ef937838f955ae04aefd640ab7f7842b2f7a1c28b4481c7686c1b8fa` | 62,527 |
| Nasdaq 2026 quarterly changes | `839e33451324f2ca51cfffbf941da9a90f9c9c8581f6b5d9920cf65703eba418` | 61,531 |
| B3 September 2021 portfolio | `b348b31f1fb97280b182c43cd156f755941e7c778570a61190633c6af1217049` | 55,085 |
| B3 September 2025 portfolio | `89c05201d0f581ec65d3c9c889aadf884cc488181157e4cb389c0226cf988ea2` | 64,269 |

The Nasdaq raw manifest SHA-256 is
`746b6668535dcffef2187dbe168bd10f05c5aeebf49364be6ca226de0d232b34`; its
canonical input hash is
`df4ac5436c306cba5184d2306fd2d527b0e3584d511b06821f53f49703bc364a`.
The B3 raw manifest SHA-256 is
`a61e1188330fb0727687a7981b403e4009e18c7877f363c8d9e4a4603366f6e1`; its
canonical input hash is
`e59cc463948e100573d9e061f2c0e8e82242c34632a485e87450ceec5aa9ba41`.

Published transitions:

| Universe | Ticker | State | `valid_from` | `available_at` | Revision |
| --- | --- | --- | --- | --- | ---: |
| `nasdaq_100` | INSM | member | `2025-12-22 14:30:00+00` | `2025-12-13 01:00:00+00` | 0 |
| `nasdaq_100` | INSM | removed | `2026-06-22 13:30:00+00` | `2026-06-12 00:00:00+00` | 1 |
| `ibovespa` | PETZ3 | member | `2021-09-06 03:00:00+00` | `2021-09-07 03:00:00+00` | 0 |
| `ibovespa` | PETZ3 | removed | `2025-09-01 03:00:00+00` | `2025-09-02 03:00:00+00` | 1 |

## Point-in-time resolution proof

The PostgreSQL resolver ordering was exercised at eight exact knowledge boundaries:

| Scenario | Expected and observed result |
| --- | --- |
| INSM immediately before addition notice availability | absent |
| INSM exactly at addition notice availability | present |
| INSM immediately before removal notice availability | present |
| INSM exactly at removal notice availability | absent |
| PETZ3 immediately before addition notice availability | absent |
| PETZ3 exactly at addition notice availability | present |
| PETZ3 immediately before removal notice availability | present |
| PETZ3 exactly at removal notice availability | absent |

The pre-removal checks used an `as_of` after the removal's market-effective instant
but a `decision_at` immediately before that removal became eligible. Retaining the
security in those two cases proves that current membership did not leak backward.

## Validation

The accepted implementation passed:

```sh
make test
go vet ./...
make historical-truth-db-test
make notebook
make dashboard-smoke
```

Focused live provider tests and both real collectors also passed. PostgreSQL stored
four immutable membership revisions with exact raw hashes, and the catalog stored a
NULL CIK for the Brazilian issuer rather than inventing an SEC identifier.

## Historical identity follow-up

This report accepted the bounded historical-membership portion only. Follow-up
commits `6331d64`, `030b506`, `54c0369`, and `19d8115` subsequently published the
source-backed INSM/PETZ3 identifier and listing intervals, added PETZ3 lifecycle
corrections, and proved exact identifier/listing resolution boundaries. See the
[combined identity/listing/membership acceptance report](2026-08-23-historical-identity-listing-publication.md).

The remaining requirement is to include these admitted rows in the final US/Brazil
v0.2 bias audit and research artifact fingerprints.
