# XNAS current/reference calendar publication — 2026-08-30

## Result

A real Nasdaq calendar collection completed successfully from checkout commit
`514ac1a0284ef427ad97c2798054aa9a852c78a0`:

```sh
INVS_BIND_ADDRESS=127.0.0.1 \
INVS_CONFIG_FILE=./config/config.local.yaml \
make ingest SOURCE=nasdaq-calendar RUN_KEY=nasdaq-calendar-2026-08-30
```

The effective local configuration enabled the new `nasdaq_calendar` provider for
the AAPL/XNAS universe. The collector run was:

| Field | Value |
| --- | --- |
| Ingestion run | `bbe1d986-e52e-4e4b-83c7-35f9204225fe` |
| Run key | `nasdaq-calendar-2026-08-30/nasdaq-calendar` |
| Status | `succeeded` |
| Started / finished | `2026-08-30T13:23:53.333156Z` / `2026-08-30T13:23:54.748934Z` |
| Records received / written / rejected | `12` / `366` / `0` |
| Raw resources / bytes | `2` / `55,801` |
| Raw run manifest | `data/raw/runs/nasdaq_calendar/bbe1d986-e52e-4e4b-83c7-35f9204225fe/manifest.json` |
| Raw run manifest SHA-256 | `d2aaa23373f80a1c6d919a57a0846a5ca091267a86e57c01cbafec4f4bdef307` |

## Canonical publication

The official [Nasdaq Trader holiday schedule](https://www.nasdaqtrader.com/Trader.aspx?id=Calendar)
was retained as raw HTML. The source object and compiled calendar are bound as
follows:

| Artifact | Value |
| --- | --- |
| Raw HTML object | `nasdaq_calendar/calendar/2026/08/30/year-2026-market_calendar/0caa3f0df811c09bf34ac182fc440538cc222ea4c307c74c25f33f3b383ab591.html` |
| Raw HTML size / SHA-256 | `54,628` bytes / `0caa3f0df811c09bf34ac182fc440538cc222ea4c307c74c25f33f3b383ab591` |
| Evidence manifest SHA-256 | `0f89a915a6ccae843e0b20e652b43346977086e07efcc7994662212896076670` |
| Calendar version | `xnas_2026_0f89a915a6cc` |
| MIC / timezone | `XNAS` / `America/New_York` |
| Coverage / sessions | `2026-01-01` through `2026-12-31` / `365` |
| Closed rows / early closes | `114` / `2` |
| Available / recorded at | `2026-08-30T13:23:54.627852Z` / `2026-08-30T13:23:54.627852Z` |
| Session fingerprint | `039ed0ea8a2a413ba23560f49501bcbc60e13b94d11e39ee90b29e1422d393d1` |
| Calendar record hash | `bbdab59d4fef25295423bbd33b303d85c17d61c70d99530d34616fc598e1603e` |

The two source-declared early closes compiled as open sessions closing at 13:00
Eastern Time:

| Session date | UTC close |
| --- | --- |
| 2026-11-27 | `2026-11-27T18:00:00Z` |
| 2026-12-24 | `2026-12-24T18:00:00Z` |

## Follow-up checks

| Check | Result |
| --- | --- |
| `INVS_BIND_ADDRESS=127.0.0.1 INVS_CONFIG_FILE=./config/config.local.yaml make reconcile` | Passed at `2026-08-30T13:25:24Z`; `issues=0` |
| `INVS_BIND_ADDRESS=127.0.0.1 INVS_CONFIG_FILE=./config/config.local.yaml INVS_BACKUP_ROOT=/home/luis/invs-backups/v1-xnas-20260830-1325 make ops-status` | Passed at `2026-08-30T13:26:31Z`; `operational_status=ok`, XNAS calendar age `0.04` hours, backup age `0.00` hours |
| `make backup` plus `make backup-validate` | Passed; `/home/luis/invs-backups/v1-xnas-20260830-1325`, `1,784` immutable files, PostgreSQL dump SHA-256 `11f2f504c723690a1a98f8a2a07e0698361d94b60ffaeb2b1b82be7c865a9ded` |

## Boundary

This accepts the source transport, parser, raw retention, catalog wiring, and
canonical XNAS session publication for a current/reference page observed at local
receipt time. The page does not provide a historical publication or correction
chronology, so this version must not be used to simulate an earlier decision. The
run also does not create a feature batch, paper ledger event, or genuine wall-clock
forward record; the v1.0 release gate remains open until the next real paper session
is captured and accepted.
