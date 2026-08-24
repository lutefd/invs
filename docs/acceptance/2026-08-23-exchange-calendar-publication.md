# Exchange-calendar publication boundary

- Date: 2026-08-23 (`America/Sao_Paulo`; live receipts are 2026-08-24 UTC)
- Provider implementation: `acd8eec` (`feat(provider): parse official exchange calendars`)
- Publication implementation: `77fee79` (`feat(data): publish versioned exchange calendars`)
- Scope: bounded, receipt-time versioned `BVMF` and `XNYS` trading sessions
- Status: **canonical current/reference publication accepted; historical calendar admission remains open**

## Decision

The collector can now retain official calendar/hour pages, compile an explicit
session row for every covered local date, fingerprint the result, and publish an
append-only `calendar_manifest` plus `trading_sessions` in PostgreSQL. The compiler:

- preserves the exchange-local date before applying its timezone;
- materializes weekends and source-declared holidays as closed sessions;
- applies source-declared late opens and early closes;
- rejects duplicate or out-of-coverage events and malformed local hours; and
- records the exact raw-evidence manifest, receipt-time availability, coverage,
  timezone, MIC, and deterministic calendar fingerprint.

This is a canonical publication boundary, not a historical-fitness claim. The
official pages used here expose their current/reference content but not a
historical publication or correction sequence. A session version therefore
becomes eligible only at this installation's raw receipt time.

## Official source boundary

- [B3 market calendar](https://www.b3.com.br/en_us/solutions/platforms/puma-trading-system/for-members-and-traders/trading-calendar/holidays/)
- [B3 equity trading hours](https://www.b3.com.br/en_us/solutions/platforms/puma-trading-system/for-members-and-traders/trading-hours/equities/)
- [NYSE trading hours and calendars](https://www.nyse.com/trade/hours-calendars)

The B3 calendar and hours are separate official resources. Because the trading-hours
page is current/reference material without an exposed effective interval, the live
BVMF acceptance deliberately covers only five dates. The NYSE page supplies both
the 2026 holiday/early-close list and its regular core session hours, so the
acceptance materializes the complete 2026 date range, still with receipt-time-only
availability.

## Live publication evidence

A temporary non-secret acceptance configuration was removed after the runs. The
first NYSE attempt stopped safely during catalog synchronization because the
temporary universe entry duplicated an existing CIK; it published no calendar
rows. The accepted runs reused the existing exact identity mapping.

| Exchange | Run ID | Coverage | Source events | Published rows | Raw objects |
| --- | --- | --- | ---: | ---: | ---: |
| BVMF | `a8d1a0c4-8821-4bf6-917f-dc4d0537cf45` | 2026-08-24 through 2026-08-28 | 15 notices plus hours | 1 manifest + 5 sessions | 3 |
| XNYS | `a751f11c-9301-4c1b-b68f-18e827c3deb7` | 2026-01-01 through 2026-12-31 | 12 notices | 1 manifest + 365 sessions | 2 |

Both exact run-key retries returned the existing successful run instead of
duplicating publication.

| Evidence | SHA-256 |
| --- | --- |
| BVMF composite calendar evidence | `2884557b2bbb5ea41e93059ce925e3af5d7bbf8028377cf42cc8f61998b198e9` |
| B3 calendar HTML | `5da572fd0deac1039ee2c82ba9f36035e172f4a226d5e1e7d1fcd6615175a67f` |
| B3 trading-hours HTML | `3f0227fc9571b11ea08ec7e0233035a91bfa5fa173c6d4c46dac99214352aecb` |
| XNYS composite calendar evidence | `3bb72eacd5953b01cb301b15b803a37f3ecac629ff374014c0ef26eeb9261200` |
| NYSE calendar/hours HTML | `49ee8a651ec01ef2866e347842c0fb11309541f247d17aeaaf7ad9d6a513b1ed` |

Published PostgreSQL state:

| MIC | Calendar version | Sessions | Fingerprint | `available_at` |
| --- | --- | ---: | --- | --- |
| BVMF | `bvmf_2026_2884557b2bbb` | 5 | `8cfae486630cb8e6f5ed10e588dffd2809a266bcd36d0841e8b9d912b43972a8` | `2026-08-24 00:53:00.696592+00` |
| XNYS | `xnys_2026_3bb72eacd595` | 365 | `58928160c862be291bc8525f5579d6c1e7ffc92f024c50c2f450729fafab9360` | `2026-08-24 00:52:39.734598+00` |

The bounded row checks confirmed:

- BVMF 2026-08-24 through 2026-08-28 are open from 10:00 to 17:00
  `America/Sao_Paulo` (13:00 to 20:00 UTC).
- XNYS 2026-11-26 is closed; 2026-11-27 is an early close from 09:30 to
  13:00 `America/New_York` (14:30 to 18:00 UTC).
- An after-close BVMF decision on 2026-08-24 resolves to the 2026-08-25
  13:00 UTC open.
- An after-close XNYS decision on 2026-11-25 crosses the Thanksgiving closure
  and resolves to the 2026-11-27 14:30 UTC open.

These decision-clock checks used the pinned calendar version rather than a
weekday heuristic.

## Validation

The accepted implementation passed:

```sh
go test ./...
go vet ./...
python3 schemas/validate_schemas.py
make test
make notebook
make historical-truth-db-test
make dashboard-smoke
```

The PostgreSQL historical-truth harness passed migrations, append-only guards,
transaction behavior, and a fresh-image check. The dashboard smoke test was run
serially afterward because both Make targets intentionally recreate the same local
PostgreSQL service.

## Remaining historical gate

Do not expose either accepted calendar to a decision time before its recorded
`available_at`. To complete the roadmap item, the project still needs:

1. official archived/versioned artifacts or another admitted source that establishes
   calendar publication, effective, and correction chronology;
2. defensible historical regular-hours intervals before broadening BVMF beyond the
   five-day current/reference boundary;
3. bounded US and Brazil audits proving that historical decisions resolve only
   calendar versions knowable at the decision time; and
4. research artifact manifests that pin the selected calendar version and decision
   clock policy.

Until those checks pass, the v0.2 exchange-calendar tracker remains open.
