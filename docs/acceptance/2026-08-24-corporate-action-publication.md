# Corporate-action publication and adjustment boundary

- Date: 2026-08-24 (`America/Sao_Paulo`)
- Policy ADR: `9442945` (`docs(adr): define corporate action adjustments`)
- Canonical publication: `8a318f0` (`feat(data): publish corporate action versions`)
- Adjustment artifacts: `7655c74` (`feat(research): publish deterministic price adjustments`)
- Exact-artifact providers: `33d8658` and `435dc51`
- Price-basis corrections: `4a3af81` and `e3ccbee`
- Supported snapshot export: `9c8ff81`
- Canonical action hashing: `4751235`
- Status: **accepted for the bounded v0.2 corporate-action and adjustment gate**

## Decision

The project now publishes append-only corporate-action revisions with exact source
evidence, resolves only versions knowable at an explicit decision time, and creates
separate immutable backward-adjusted price artifacts. The accepted boundary proves:

- one exact SEC AAPL four-for-one split, knowable at the EDGAR acceptance timestamp;
- one five-version B3 UP2DATA sample cash-dividend family retained only from local
  installation receipt and left `unsupported` because the sample does not establish
  the action-state semantics or historical product-delivery chronology;
- exact split and cash-dividend arithmetic under
  `backward_split_dividend_1_0_0`, with unsupported actions blocking the complete
  output interval;
- deterministic action snapshots, action-record hashes, artifact IDs, Parquet
  bytes, validation, and exact replay;
- raw price inputs and adjusted outputs remaining separate and content-addressed;
  and
- Yahoo chart prices classified as `split_adjusted`, never as eligible raw inputs
  to this policy.

This is a bounded mechanism, not a broad US or Brazil corporate-action archive. The
B3 sample is installation-replay evidence, not authorized historical UP2DATA
delivery. Cash-dividend computation is covered by exact deterministic fixtures; the
accepted live SEC event is the split, while the live B3 cash family remains a
deliberate blocking state.

## Official source boundary

### SEC EDGAR

- [Apple 2020-07-30 8-K filing index](https://www.sec.gov/Archives/edgar/data/320193/0000320193-20-000060-index.htm)
- [Apple Exhibit 99.1](https://www.sec.gov/Archives/edgar/data/320193/000032019320000060/a8-kexhibit991q3202062.htm)

The filing was accepted at `2020-07-30T22:55:04Z`. The exhibit declares the
four-for-one split, the August 24 record date, and August 31 split-adjusted trading.
The collector retains both exact resources plus a composite evidence manifest before
publishing revision 0.

### B3 UP2DATA public sample

- [Corporate Action sample ZIP](https://b3.com.br/data/files/CE/F5/F6/71/2643881036DB3088AC094EA8/Eventos%20Corporativos-Corporate%20Action.zip)

The retained lifecycle member contains five source rows for event family
`1E12917D1E504313951A81787F2272A3`. Revisions 0 through 4 preserve the source report
order and amounts. Every revision is available only at the local installation
receipt. The latest revision remains `unsupported`; the adjustment engine therefore
emits a diagnostic and no artifact.

## Exact evidence

| Evidence | SHA-256 |
| --- | --- |
| SEC filing index | `5f9c8d0147717462c54c698f6029d32d3f6fd3b9502e6794e715d15b0105c8ab` |
| SEC Exhibit 99.1 | `6830ad7c65b270a42e37fc59f3056f3d3f1014c19c57cd595efadd472d46cd85` |
| SEC composite evidence | `abe3ec104c5f685d1097bcba4ce441f6983b628960d5a215f03d9d3d4dee0dae` |
| B3 sample ZIP | `b23299941df165d860b1289edc3fdfebb2b90772ea430160f37085586030a053` |
| B3 composite evidence | `5dd1e36a6c4fd7a44a7530831afd76063696abd3d3051f2b018884be44ffb5bc` |
| Supported SEC action snapshot | `1c734b578366b5e879b599eb55df4d12fbd1e0d6537e03c2228e89db6bb9743e` |
| Explicit raw AAPL acceptance manifest | `362a7fd37d8e572b3a34eeda1debe3e8cda9689ca368c8a7d0470594d288c821` |
| Accepted adjustment manifest | `3c494fe1be43094c9c610542f2785710bd12b27a5480cd60e9b3ee81191b26bb` |
| Accepted adjustment Parquet | `6a3089bb5ce6e55337562b1d916661a87df356a175c23f817fadb2f12017ad9b` |

The retained acceptance directory is
`/home/luis/invs-acceptance/2026-08-24-corporate-actions`; its isolated database is
`invs_v02_action_acceptance_20260824`. Initial live publication used collector image
`invs-collector@sha256:ea2ae7b06a56dcd6455491ca9f6d4dd9219d1d9dae4b32232dc942b213a983d4`.
The final snapshot export and exact-key replay used pinned commit `4751235` in
`invs-collector@sha256:8f0c5fa3d863085e61b21be7311af9922e52c9f21c1f370d837d1abd3b821eba`.

## Live publication evidence

| Provider | Run ID | Status | Received / written | Raw objects | Raw run manifest |
| --- | --- | --- | ---: | ---: | --- |
| SEC EDGAR | `a75cb676-0b12-49d2-8247-8546eb4bfead` | succeeded | 3 / 1 | 3 | `26e5580860bfcdf15f341128e68555a80fa8ef23882cefecfbb05e3425ec9502` |
| B3 installation replay | `66601ae1-34c5-4e95-b2ca-90dc9910173d` | succeeded | 6 / 5 | 2 | `608e706c29fd543dcf2b6f1871bc91df2809a25558f397241fc735fdf4034a29` |

PostgreSQL checks confirmed that the SEC family resolves to zero rows one second
before acceptance and one row exactly at acceptance. B3 resolves to zero rows before
local receipt and revision 4 `unsupported` at receipt. Both exact run keys returned
their existing successful runs without new fetches or rows when replayed by the
final pinned image.

## Adjustment evidence

The accepted artifact is
`artifact_id=48480a08-2dc9-58d5-928a-4a3b349866f2`. Its explicit raw fixture exists
only to exercise the live SEC action against unadjusted prices; it is not market-data
source admission. The three output rows prove:

| Observation | Raw close | Price factor | Adjusted close | Raw volume | Adjusted volume |
| --- | ---: | ---: | ---: | ---: | ---: |
| 2020-08-28 | 400 | 0.25 | 100 | 100 | 400 |
| 2020-08-31 | 100 | 1 | 100 | 400 | 400 |
| 2020-09-01 | 104 | 1 | 104 | 410 | 410 |

The selected action record hash is
`3f4ca43ac1375de2d0e64d8b93be7914aaa341e406a589f7f26fb69c88d889ab`;
the ordered action-snapshot hash is
`54917b95c713846f8fd35b1f9c5df9ecbde920af679e0f6ccd607f16fe4e97b0`.
Equivalent `Z` and `+00:00` input timestamps produce the same record hash and
artifact ID. Replaying the supported action snapshot returned the same manifest and
bytes. The B3 revision produced `unsupported corporate action ... blocks adjustment`
and no output directory.

An earlier acceptance probe exposed non-canonical timestamp lexemes in action record
hashing. Its artifact was rejected and moved intact under
`rejected/precanonical-action-hash/`; it is not an accepted artifact.

## Price-basis correction

The retained 2026-08-12 Yahoo AAPL sample proves the provider's 2020 pre-split OHLC
and volume are already split-normalized. New Yahoo rows are therefore
`split_adjusted`. Migration `000008_price_basis` admits explicit price bases in the
latest projection. The normalizer rejects legacy Yahoo rows labeled `raw`, and the
adjustment engine independently rejects them. Existing immutable legacy manifests
must be archived and reingested; they must not be rewritten in place.

## Validation

The boundary passed:

```sh
make test
make historical-truth-db-test
docker compose build collector
make action-snapshot ...
python -m research.adjustment_cli publish ...
python -m research.adjustment_cli validate ...
```

The final suite contained all Go tests and vet, 18 JSON Schemas, 69 Python tests,
and Ruff. The PostgreSQL harness covered repeated forward migrations, append-only
action rows, rollback/reapply, and fresh-image initialization through migration
`000008`.

## Remaining boundary

The next v0.2 unit is versioned FX observations and the canonical USD/BRL conversion
policy. Canonical SEC filing metadata, the bounded Brazil price bridge, and the final
US/Brazil point-in-time bias audits remain open. B3 production action history remains
unavailable until authorized delivery evidence or another admitted source supplies
the required chronology and state semantics.
