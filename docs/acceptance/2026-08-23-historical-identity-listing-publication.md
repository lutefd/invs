# Historical identity, listing, and membership publication boundary

- Date: 2026-08-23 (`America/Sao_Paulo`; live receipts are 2026-08-24 UTC)
- Correction-resolution fix: `6331d64` (`fix(data): apply corrections before validity filters`)
- B3 lifecycle provider: `030b506` (`feat(provider): parse B3 listing lifecycle notices`)
- Initial identity publication: `54c0369` (`feat(data): publish index-backed historical listings`)
- Lifecycle correction publication: `19d8115` (`feat(data): publish B3 listing lifecycle corrections`)
- Scope: INSM in the Nasdaq-100 and PETZ3 in Ibovespa, including PETZ3 trading cessation
- Status: **bounded US/Brazil historical identity, listing, and membership publication accepted**

## Decision

The accepted collectors now turn official index notices into initial source-backed
identifier, primary-listing, and universe-membership rows. An official B3 trading
cessation notice then publishes revision-1 PETZ3 identifier and listing rows whose
exclusive `valid_until` is the first non-trading instant.

Resolvers first select the latest correction family knowable at `decision_at` and
only then apply its validity interval at `as_of`. That ordering is necessary: before
the lifecycle notice was public, PETZ3 remains resolvable after the eventual
delisting instant; exactly when the notice becomes knowable, the corrected interval
applies and PETZ3 is absent after delisting. Equal-rank conflicting corrections fail
closed.

This closes the roadmap's bounded historical identifier/listing/membership
publication item. It does not claim a complete security master, broad constituent
history, accepted Yahoo price transport, or v0.2 completion. The final US/Brazil
bias audit still has to combine this boundary with calendars, actions, FX, macro
vintages, prices, and filings.

## Official source boundary

### United States

- [Nasdaq-100 annual changes published 2025-12-12](https://www.globenewswire.com/news-release/2025/12/13/3204942/6948/en/annual-changes-to-the-nasdaq-100-index.html)
- [Nasdaq-100 June 2026 quarterly changes](https://www.globenewswire.com/news-release/2026/06/12/3310860/0/en/nasdaq-100-index-june-2026-quarterly-changes.html)
- [SEC filing identity for Insmed, CIK 0001104506](https://www.sec.gov/Archives/edgar/data/1104506/000110450626000009/0001104506-26-000009-index.html)

The Nasdaq-authored releases establish INSM's ticker, Nasdaq-100 membership, exact
release clock, and effective market-open dates. The configured stable security and
issuer IDs join that evidence to the admitted SEC identity. Publication creates an
XNAS/USD primary-listing interval and an XNAS-scoped ticker interval alongside the
membership add/remove chain.

### Brazil

- [B3 September 2021 Ibovespa portfolio](https://www.b3.com.br/pt_br/noticias/b3-divulga-nova-carteira-de-ibovespa-b3-e-demais-indices.htm)
- [B3 September 2025 Ibovespa portfolio](https://www.b3.com.br/pt_br/noticias/ibovespa-8AA8D0CD9851B974019905C93D246CB2.htm)
- [B3 notice that PETZ3 trading ceased from 2026-01-05](https://sistemasweb.b3.com.br/PlantaoNoticias/Noticias/Detail?agencia=18&dataNoticia=2026-01-02+19%3A43%3A10&idNoticia=3192104)
- [CVM open-company register](https://dados.cvm.gov.br/dados/CIA_ABERTA/CAD/DADOS/cad_cia_aberta.csv)

The portfolio notices establish the PETZ3/B3 membership chain. The Plantao notice
states that PETZ shares cease trading on 2026-01-05 because of incorporation and
provides an exact publication clock of 2026-01-02 19:43:10
`America/Sao_Paulo`. The parser therefore publishes `available_at`
`2026-01-02T22:43:10Z` and exclusive `valid_until` `2026-01-05T03:00:00Z`.
The configured stable identity maps Pet Center to CVM code `25089`, BVMF, and BRL;
it intentionally has no SEC CIK.

## Exact acceptance configuration and runtime

The temporary non-secret YAML had SHA-256
`a7680ee2cbe0e01d54de0fc16c85d53dad1cb6bc614a1db40402a11c3935f2bb`
and was removed after acceptance. It enabled only the two membership collectors and
the single PETZ3 lifecycle notice. The mappings were:

| Source | Security | Stable security ID | Market identity |
| --- | --- | --- | --- |
| Nasdaq | INSM | `c16dc49f-7817-41e9-8101-f0a118d7103d` | US / XNAS / USD / CIK 0001104506 |
| B3 | PETZ3 | `fcb3f84d-e8e8-46ad-aace-70027962523f` | BR / BVMF / BRL / CVM 25089 / no SEC CIK |

The collector image was built from full Git SHA
`19d81152b498a8f770bae020711f7b573f139b14`; its immutable image ID and local
repo digest were
`sha256:669e34c7d365076d82c8edd7d7dcfff4d26ab2a827cddf9cf4e9f41e9ea42761`.

Acceptance used a clean, migration-000006 database named
`invs_v02_identity_acceptance`. This kept the earlier membership-only acceptance
rows immutable while proving the entire membership-to-identity-to-correction
sequence from an empty canonical boundary.

## Live publication evidence

| Collector | Run ID | Stable run key | Received | Published | Rejected | Raw objects | Manifest SHA-256 | Input SHA-256 |
| --- | --- | --- | ---: | ---: | ---: | ---: | --- | --- |
| Nasdaq membership and identity | `c2b272fa-611a-4eae-a7ad-316dd577d807` | `identity-us-acceptance-20260824-v1/nasdaq-membership` | 22 | 4 | 0 | 2 | `6e3fcede8f73e3493a48d5612267ce2330cdea7d48d0af6660fd0c2bf63ae28e` | `df4ac5436c306cba5184d2306fd2d527b0e3584d511b06821f53f49703bc364a` |
| B3 membership and initial identity | `884a43af-0498-44b1-98a7-7b9d63291f64` | `identity-br-membership-acceptance-20260824-v1/b3-membership` | 11 | 4 | 0 | 2 | `6a20d07609fc3c55e5611ba77273a1d0f43318a7c1f4ec881155e8fedff04385` | `e59cc463948e100573d9e061f2c0e8e82242c34632a485e87450ceec5aa9ba41` |
| B3 lifecycle correction | `71f00ba9-786a-44cb-969d-acffff92a795` | `identity-br-lifecycle-acceptance-20260824-v1/b3-listing-history` | 1 | 2 | 0 | 1 | `d9810d155dcdc571b1a924efde034139548ecd26771da9bfd3ee8b04ac8fc7a1` | `d9707e84d962ba49dde4be05f6052ca04bd698921bb18c5c6d59a16357ce8630` |

All three runs finished `succeeded`. Repeating every exact key returned its existing
terminal run and logged `collector run already terminal; skipping`; PostgreSQL
still contained exactly three runs.

Raw evidence retained by the clean run:

| Evidence | Raw SHA-256 | Bytes |
| --- | --- | ---: |
| Nasdaq 2025 annual changes | `d7e51501ef937838f955ae04aefd640ab7f7842b2f7a1c28b4481c7686c1b8fa` | 62,527 |
| Nasdaq 2026 quarterly changes | `839e33451324f2ca51cfffbf941da9a90f9c9c8581f6b5d9920cf65703eba418` | 61,531 |
| B3 September 2021 portfolio | `0f7fe9cbb0d4e24f5087aaf23b4246a2a8bc68d1d0a284fc28b4dc6c5741c21a` | 55,085 |
| B3 September 2025 portfolio | `20ba55de1ec047c340b0c7de138e17e4431ff349da73d4530dc278089d348c7c` | 64,269 |
| B3 PETZ3 trading-cessation notice | `53fd3394f7b69c3e5e19d72f4559d8e6d4607c7dbc2f6159ae2ee9954952115e` | 8,983 |

The two B3 portfolio pages include a Cloudflare transport script whose token changes
between independent downloads. The clean-run byte hashes therefore differ from the
earlier membership-only acceptance even though the source facts and byte counts are
unchanged. Raw bytes are preserved exactly, and stable run-key retries do not
refetch or silently rewrite them.

## Canonical revision evidence

| Security | Record | Revision | `valid_from` | `valid_until` | `available_at` |
| --- | --- | ---: | --- | --- | --- |
| INSM | XNAS ticker and NASDAQ/XNAS/USD listing | 0 | `2025-12-22T14:30:00Z` | open | `2025-12-13T01:00:00Z` |
| PETZ3 | BVMF ticker and B3/BVMF/BRL listing | 0 | `2021-09-06T03:00:00Z` | open | `2021-09-07T03:00:00Z` |
| PETZ3 | corrected BVMF ticker and B3/BVMF/BRL listing | 1 | `2021-09-06T03:00:00Z` | `2026-01-05T03:00:00Z` | `2026-01-02T22:43:10Z` |

Every canonical row carries its exact raw payload hash, immutable record hash,
source reference, run-start `recorded_at`, and explicit data-source authority. The
lifecycle publisher refuses to create revision 1 unless the exact open-ended
revision-0 identifier and listing bases both exist.

## Point-in-time resolution proof

The actual Go PostgreSQL resolvers passed these exact checks against the clean
acceptance database:

| Scenario | Expected and observed result |
| --- | --- |
| INSM immediately before its addition notice is knowable | identifier absent |
| INSM exactly when its addition notice becomes knowable | identifier present; NASDAQ/XNAS/USD listing present |
| PETZ3 after eventual delisting, immediately before the lifecycle notice is knowable | revision-0 identifier still present |
| PETZ3 after eventual delisting, exactly when the lifecycle notice becomes knowable | identifier absent |
| PETZ3 one microsecond before trading cessation, after the notice is knowable | revision-1 identifier present |
| PETZ3 after trading cessation, after the notice is knowable | listing absent |
| PETZ3 membership immediately before its removal notice is knowable | present |
| PETZ3 membership exactly when its removal notice becomes knowable | absent |

The acceptance-only Go test and YAML were deleted after the run; neither is part of
the product or committed repository surface.

## Validation

The implementation and acceptance boundary passed:

```sh
make test
go vet ./...
make historical-truth-db-test
make notebook
make dashboard-smoke
```

The live PostgreSQL resolver test, all three real collector runs, manifest hash
checks, canonical-row queries, and exact-key retry checks also passed.

## Remaining v0.2 gate

Historical identity/listing/membership publication is accepted only for the bounded
US and Brazil cases above. The next required unit is historical calendar
publication/effective/correction chronology plus decision-clock artifact pinning.
Corporate actions, adjustment artifacts, FX, filing metadata, Yahoo bridge terms,
and the combined point-in-time bias audits remain open.
