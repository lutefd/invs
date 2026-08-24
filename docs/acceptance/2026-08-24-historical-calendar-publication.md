# Historical calendar and decision-clock boundary

- Date: 2026-08-24 (`America/Sao_Paulo`)
- Admission ADR: `86a2c72` (`docs(adr): define historical calendar admission`)
- Provider implementation: `beba0a1` (`feat(provider): verify historical calendar artifacts`)
- Publication implementation: `173023e` (`feat(data): publish historical calendar artifacts`)
- Research pin implementation: `d7f4e18` (`feat(research): pin calendar decision clocks`)
- Nasdaq hours admission fix: `6193dd0` (`fix(provider): admit Nasdaq historical session hours`)
- Scope: bounded historical `XNAS` and `BVMF` calendar chronology
- Status: **accepted for the v0.2 US/Brazil calendar and decision-clock gate**

## Decision

The project now admits exact, immutable official calendar artifacts with declared
knowledge times, deterministic full-version publication, and explicit session rows.
The accepted boundary proves:

- a US early-close/holiday interval from a stable Nasdaq annual calendar plus a
  dated official session-hours guide;
- a Brazil closure/special-hours interval from an original B3 circular and its
  later correction circular;
- selection of only the calendar version knowable at the decision timestamp;
- exact-key retry without duplicate publication;
- rejection and raw retention when live bytes disagree with a configured hash;
- half-open session boundaries and `after_close_next_session` execution; and
- deterministic research-artifact pins containing the selected source, version,
  session fingerprint, knowledge time, and clock policy.

This accepts a bounded historical mechanism, not a complete archive for every US
or Brazil trading date. Research outside the published coverage must fail closed or
admit another exact source artifact first.

## Official source boundary

### Nasdaq

- [Trading Calendar 2024](https://www.nasdaqtrader.com/content/technicalsupport/2024tradingcalendar.pdf)
- [Order Entry Reference Guide](https://www.nasdaqtrader.com/content/productsservices/trading/oe_refguide.pdf)
- [Equity Trader Alert 2024-84](https://nasdaqtrader.com/TraderNews.aspx?id=ETA2024-84)

The annual calendar is a stable official PDF that identifies December 24 as a
13:00 ET early close and December 25 as an exchange holiday. The retained Order
Entry Reference Guide is a stable official PDF created in June 2018 and establishes
the 09:30-16:00 ET regular market session. The dated December 12, 2024 alert links
the annual calendar and establishes a conservative public-knowledge boundary; the
calendar version is eligible from the next New York local midnight,
`2024-12-13T05:00:00Z`. The alert HTML itself is dynamic and is not canonical
calendar evidence.

### B3

- [OC 054-2025-VNC](https://b3.com.br/data/files/C2/E3/28/AD/4FAEA9105B12E5A9AC094EA8/OC%20054-2025-VNC%20CALENDARIO%20DE%20FERIADOS%20EM%202026%20E%20FUNCIONAMENTO%20DA%20B3%20EM%2018022026%20QUARTAFEIRA%20DE%20CINZAS_PT.pdf)
- [OC 003-2026-VNC correction](https://www.b3.com.br/data/files/FC/55/3B/12/7FE9B9109B5E99B9AC094EA8/OC%20003-2026-VNC%20ERRATA_CALENDARIO%20DE%20FERIADOS%20EM%202026%20E%20FUNCIONAMENTO%20DA%20B3%20EM%2018022026%20QUARTAFEIRA%20DE%20CINZAS_PT.pdf)

The original circular is eligible at `2025-12-05T03:00:00Z`; the correction is
eligible at `2026-01-09T03:00:00Z`. Those are conservative next-midnight Sao Paulo
boundaries after the official publication dates. Both circulars describe the same
bounded listed-equity sessions: Carnival Monday and Tuesday closed, followed by a
13:00-18:00 local session on Ash Wednesday. The later correction concerns another
service. It therefore creates a distinct immutable calendar source version while
retaining the same selected equity-session fingerprint.

## Exact artifact evidence

| Artifact | SHA-256 |
| --- | --- |
| Nasdaq Trading Calendar 2024 PDF | `5a417265c706abe1e2eaa24e0c1fde1787e79fdd472eca105466a019f8ff1101` |
| Nasdaq Order Entry Reference Guide PDF | `c22943f87b15bd788b923739c55daacd80a228e968d6ee2b0fc1e4ed1a875a2f` |
| Final XNAS composite evidence | `9065027bbd1b7b7ab502cd1a3074197e0f3474d50cbf9faca7643625a3117298` |
| B3 OC 054-2025-VNC PDF | `4ba01ad1dafc8caa2def925e006e5d7631e8fc7906f33334265e0a9889a2d44a` |
| B3 original composite evidence | `6798f485ce7b07d2c90ebba3bbf224bd41634f6ffbd48850f9d927d5bc1cb54a` |
| B3 OC 003-2026-VNC correction PDF | `744cb8b9f4b2c2ddaca075b4cbb4a8ee21fd6fe32a4eee9480b6b1b586fdd028` |
| B3 correction composite evidence | `3621838ebc37953004a90225d45dc476daf3f47407b874fca1b8d756e1134377` |

The retained acceptance directory is
`/home/luis/invs-acceptance/2026-08-24-historical-calendar`; its clean database is
`invs_v02_calendar_acceptance_20260824`. The accepted collector image is
`invs-collector@sha256:505243e9cddb0c7b4a2c3e568c0faf41fbfae364d2ae2f3ecc6fe267ea708792`.

## Live publication evidence

| Provider | Run ID | Status | Received / written | Raw objects | Raw manifest |
| --- | --- | --- | ---: | ---: | --- |
| Nasdaq dynamic-alert probe | `72c6c9c2-cbef-4fde-9112-15631a45dfbc` | partial | 0 / 0 | 1 | `a5c5000f3278299123379781e40276237b8c0cb885459f27db64786593625a81` |
| Nasdaq final official-PDF bundle | `ca06f23a-9aeb-499f-849f-cae40a2dec68` | succeeded | 4 / 3 | 3 | `fb3cc8fa12c5334e71a5a5f4f763c41f04f058bbce496eda15e09c2dbc804729` |
| B3 original plus correction | `f4acf1d1-23be-4e55-83b1-5cca4e149f27` | succeeded | 8 / 8 | 4 | `1488ceacae3696a893ef5a7c06bbbc9eb50180fd475b609b2fbfb6e49663f44b` |

The dynamic Nasdaq alert probe fetched hash
`3c04ead1848b88c8a907b3f2a72f5bf148655612e8abb8546232a8c7e72c5b86`,
which disagreed with the configured hash. The collector retained the bytes and run
manifest, marked the run partial, and published no canonical rows. This is the
expected fail-closed behavior for mutable HTML. Exact-key retries of both accepted
PDF-backed runs returned their terminal successful runs without fetching or writing
again.

## Published chronology and sessions

| MIC | Calendar version | `available_at` | Sessions | Session fingerprint |
| --- | --- | --- | ---: | --- |
| XNAS | `xnas_2024_9065027bbd1b` | `2024-12-13T05:00:00Z` | 2 | `dddb522ced27f59bc1e2f39c4f7bb9ea46ffd60c3113df8c97d7be78a2d74495` |
| BVMF | `bvmf_2026_6798f485ce7b` | `2025-12-05T03:00:00Z` | 3 | `046cf91ea283d690fb6c08407c63d592b42840f01845d90e42ae916937dd7773` |
| BVMF | `bvmf_2026_3621838ebc37` | `2026-01-09T03:00:00Z` | 3 | `046cf91ea283d690fb6c08407c63d592b42840f01845d90e42ae916937dd7773` |

PostgreSQL row checks confirmed:

- XNAS December 24 opens at `2024-12-24T14:30:00Z`, closes early at
  `2024-12-24T18:00:00Z`, and is marked `is_early_close=true`;
- XNAS December 25 is explicitly closed;
- both BVMF versions explicitly close February 16 and 17 and open February 18
  from `2026-02-18T16:00:00Z` to `2026-02-18T21:00:00Z`;
- a `2026-01-01T00:00:00Z` decision selects the original B3 version, while a
  `2026-01-10T00:00:00Z` decision selects the correction; and
- the Python decision-clock resolver selects the February 18 BVMF open from the
  explicit February 17 closure, while rejecting an XNAS decision one microsecond
  before the December 24 close as still inside the current session.

## Research artifact pin

Feature artifact contract 1.1 requires this complete envelope:

```text
data_source_id
mic
calendar_version
session_fingerprint
calendar_available_at
decision_clock_policy = after_close_next_session
```

The pin contributes to input availability, the input fingerprint, and default
artifact identity. Missing, malformed, or differently fingerprinted pins fail
validation. The feature registry itself remains `market-basic` 1.0.0; only the
artifact contract advanced.

## Validation

The accepted boundary passed:

```sh
go test ./...
go vet ./...
python3 schemas/validate_schemas.py
make test
make historical-truth-db-test
make notebook
make dashboard-smoke
```

## Remaining boundary

The next v0.2 unit is corporate actions and reproducible adjustment artifacts.
Calendar coverage may later be broadened only with additional exact official
artifacts and explicit knowledge-time admission; absence of coverage is not
permission to infer weekdays or use the newest calendar retroactively.
