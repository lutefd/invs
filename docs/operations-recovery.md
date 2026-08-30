# Recovery, reconciliation, and daily operations

The v1.0 operator boundary is an explicit collect-to-paper cycle. It preserves the
v0.x commands below while adding a manifest-driven runner that records each stage,
its dependency outcome, and its log path. The cycle specification and report are
strict versioned contracts; do not replace them with an ad-hoc shell sequence.

This runbook covers the durable foundation only. It does not make a historical
truth or trading-system claim. The reconciliation and restore implementation was
accepted at `0f73e39`; the full v0.1 foundation and operations gate was accepted at
`63d479d`. The exact evidence is in
[the v0.1 foundation acceptance report](acceptance/2026-08-13-v0.1-foundation.md).

## Operational objectives

The local host scheduler is the alert router for this release. A command exits with
attention rather than silently converting an incomplete run into success; cron or a
systemd timer should alert on a non-zero exit and retain the corresponding log.
There is deliberately no remote notification dependency in v1.0.

The default objectives are:

| Signal | Objective | Operator surface |
| --- | --- | --- |
| Enabled-source run freshness | A successful run in the last 26 hours for each source scheduled by the local daily cycle | `make ops-status` and `logs/` |
| Latest price/macro projection | No more than 26 hours old | `make ops-status` and Grafana |
| Unresolved failed/partial runs | Zero after the latest successful run for each enabled source | `make ops-status` and PostgreSQL run lineage |
| Complete-cycle window | Finish within 120 minutes of the scheduled start | Daily-cycle report `started_at`/`updated_at` |
| Backup age | A validated backup no more than 30 hours old | `INVS_BACKUP_ROOT` plus `make ops-status` |
| Restore objective | Restore the latest accepted backup into a clean root within 15 minutes, with at most one scheduled cycle of evidence loss | Recovery drill report |
| Disk headroom | Warn at 85% used; stop new writes and recover capacity before 95% | `make ops-status` |
| Paper reconciliation | Zero ledger/account projection findings before the next cycle | `paper-reconcile` and cycle report |

`INVS_STALE_AFTER_HOURS`, `INVS_PROJECTION_AFTER_HOURS`,
`INVS_BACKUP_AFTER_HOURS`, and `INVS_DISK_WARN_PERCENT` are local threshold
overrides. Change them only when the source cadence and the reason are recorded in
the operator log. `INVS_BACKUP_ROOT` may point at one backup directory or at a
dedicated directory containing backup directories; it must not point at a broad
filesystem root. The complete daily-cycle runner sets it to the exact backup it
created or validated, so the final observation checks backup age.

Run the executable security and backup contract checks before accepting a host
configuration:

```sh
make security-check
make backup-restore-acceptance
```

The security check never prints values from `.env` or local configuration. It checks
the effective Compose host bindings, Grafana authentication defaults, local secret
file permissions, Git tracking, and high-confidence secret/contact patterns in
tracked documentation/release artifacts and retained logs. A deliberate non-loopback
override fails the check until its authentication and network review are complete.

The v1 recovery and historical-bias challenge suite exercises the retained
migration, bias, paper-ledger, daily-resume, backup-integrity, and live clean-root
restore paths:

```sh
make v1-resilience-acceptance
```

It writes the ignored runtime report under `data/research/acceptance/v1/` and keeps
the temporary restored database disposable. The accepted result is recorded in the
[v1 resilience acceptance report](acceptance/2026-08-30-v1-resilience.md). This is
local operational evidence and does not create the genuine wall-clock forward paper
record required for the v1.0 release gate.

## Capture genuine forward paper evidence

Once a real paper session has completed, capture its recent reconciled evidence
from the ledger root:

```sh
make forward-record-capture \
  FORWARD_LEDGER_ROOT=data/research/forward/v1/ledger \
  FORWARD_ACCOUNT_ID=<account-id> \
  FORWARD_OUTPUT=data/research/forward/v1/forward-record.json
```

This command validates the immutable account, append-only ledger, latest paper
report, and adjacent manifest before writing a hash-pinned forward record. The
report must be no more than seven calendar days old, and an existing output may
only be reused when its bytes are identical. Do not run it against the retained
v0.6 reproduction tree: replay evidence is intentionally not accepted as a
wall-clock forward record.

## Reconcile before and after operations

Run the full report from the repository root:

```sh
make reconcile
```

The command mounts the repository `data/` tree into the collector container and
reads PostgreSQL metadata. It is read-only. Exit codes are:

- `0`: no findings;
- `1`: a finding was reported with `--fail-on-issues`;
- `2`: the report could not be configured or executed.

For machine-readable output, run the same binary through Compose:

```sh
docker compose --profile collect run --rm collector \
  reconcile --data-root /data --json --fail-on-issues
```

The report checks:

- queued/running ingestion runs, with an operator-review recommendation only;
- raw run manifests, every listed raw object, and the PostgreSQL manifest hash;
- raw manifests that have no terminal PostgreSQL run;
- normalized manifests, missing or hash-mismatched listed parts, and unlisted
  Parquet files;
- feature-manifest structure, output parts, unlisted artifact files, and each
  selected normalized input manifest/part, including the required calendar pin
  and its input-fingerprint contribution.

The report validates feature files and their selected inputs, but it does not compare
PostgreSQL `feature_artifacts` registrations with the feature root. For a published
dataset-level batch, run `make feature-catalog` after the filesystem checks; that
command revalidates the batch and registers its metadata idempotently. To inspect the
registered metadata without reading feature values, use the separate read-only
`make feature-report` command. Automatic catalog orphan repair and cross-store
reconciliation remain later operations work. Research document, event, evidence-pack,
and memo files are covered by the backup manifest and their own content/hash
validators; `make research-acceptance` exercises the complete local research path.

The report never cancels a run, deletes an orphan, or rewrites evidence. If an
active run is confirmed orphaned, use the existing explicit collector command
with an exact identity and reason:

```sh
docker compose --profile collect run --rm collector \
  --cancel-run --cancel-source fred \
  --cancel-run-key '<exact-run-key>' \
  --cancel-reason 'operator confirmed orphan after inspection'
```

`--filesystem-only` is useful while inspecting an immutable restore before its
metadata database is available:

```sh
docker compose --profile collect run --rm collector \
  reconcile --data-root /data --filesystem-only --json
```

Raw evidence copied without its PostgreSQL metadata will intentionally appear as
unpaired in this mode. A restored database is required for the zero-finding
durable-state check.

## Backup

Back up to a new, not-yet-existing directory. The script refuses to overwrite an
existing destination:

```sh
make backup BACKUP_DIR=/home/luis/invs-backups/$(date -u +%Y%m%dT%H%M%SZ)
```

The backup contains:

- a plain PostgreSQL dump from the running Compose database;
- `immutable/raw/`, `immutable/normalized/`, `immutable/features/`, and
  `immutable/research/`;
- the PostgreSQL database dump, including feature, experiment, and paper-account
  catalog/projection rows;
- `backup-manifest.txt` with file sizes and SHA-256 hashes;
- the effective Git commit and a SHA-256 fingerprint of
  `INVS_CONFIG_FILE` (the configuration itself is not copied).

The backup is assembled in a private staging directory and renamed into place only
after the dump and file manifest are complete. It refuses an existing destination,
symlinked immutable input, and a destination inside the checkout. The validator also
rejects group/world-readable backup roots, symlinks, duplicate or unlisted immutable
files, unsafe manifest paths, and hash mismatches.

The backup does not source or print `.env` values and does not include database
passwords. Keep the backup directory outside the checkout and apply the host's
separate retention policy to it. Raw and canonical evidence are retained; old
backups, logs, temporary files, and unlisted evidence require explicit operator
review before removal.

## Restore to a clean root

Restoration is intentionally destination-based and non-destructive:

```sh
backup_dir=/home/luis/invs-backups/<backup>
restore_dir=/tmp/invs-restore-$(date -u +%Y%m%dT%H%M%SZ)
restore_db=restore_$(date -u +%Y%m%d%H%M%S)

make restore \
  BACKUP_DIR="$backup_dir" \
  RESTORE_DIR="$restore_dir" \
  RESTORE_DB="$restore_db"
```

The restore script requires an absent filesystem destination, validates the backup
before copying, and verifies every copied hash again. It copies into a private
staging directory and publishes the clean root only after all file checks pass.
When `RESTORE_DB` is provided it must begin with `restore_`; the script creates
that new database and never drops or overwrites the configured application
database. The order is:

1. verify and copy immutable raw, normalized, feature, and research files;
2. create and load the explicitly named restore database;
3. run reconciliation against the restored data root and restored database;
4. run read-only DuckDB/catalog, feature-validation, notebook, and dashboard
   checks for the restored slice.

The restore script performs steps 1 and 2. Steps 3 and 4 are explicit operator
verification commands because the restored database URL and the intended research
slice are deployment-specific; a successful file copy alone is not a recovery
acceptance.

Set a database URL for the temporary database without printing it in logs:

```sh
export DATABASE_URL='postgres://<local-user>:<local-password>@127.0.0.1:<port>/<restore_db>?sslmode=disable'
go run ./cmd/reconcile \
    --data-root "$restore_dir/data" \
    --database-url "$DATABASE_URL" \
    --json --fail-on-issues
```

For a restored catalog check, mount the restored data read-only into Jupyter and
inspect `ResearchCatalog.status()`; use the restored feature manifest with
`read_feature_artifact()`, then run `make feature-report` against the restored
PostgreSQL state. Dashboard smoke checks must target the restored database, not the
original application database.

## Daily host schedule

The scheduler remains host-level. Do not add Dagster or Prefect for this boundary.
For the complete v1.0 path, provide a strict cycle specification and run:

```sh
make daily-cycle CYCLE_SPEC=/absolute/path/to/daily-cycle.json
```

The specification must declare one UTC session date, one collection source and run
key, the feature-batch universe/schedule/calendar/registry inputs, at least one
paper account specification, a ledger location, and a new backup destination
outside the checkout. The runner takes the shared `.runtime/daily.lock`, runs the
fixed order preflight → collection → reconcile → feature batch → paper account
cycles → backup → final reconcile → observation, and writes its report under the
declared `report_path`. A failed dependency skips only its downstream stages;
`make ops-status` still runs for diagnosis. The backup stage is gated only by release
preflight, so it still preserves partial raw/derived evidence after a collection,
feature, or paper-stage failure. Rerunning the same specification resumes successful
stages and uses `backup-or-validate` so an already-created backup is never overwritten.

The v0.x-compatible `make daily` wrapper remains available for collection,
reconcile, and status-only maintenance runs. It serializes the batch with the
same stable UTC date-key convention, but it is not the v1.0 complete-cycle gate.

The default schedule is:

```cron
15 02 * * * cd /home/luis/dev/invs && make daily >> /home/luis/dev/invs/logs/cron.log 2>&1
```

The legacy wrapper writes one log per UTC run under `logs/` and takes
`.runtime/daily.lock` with `flock`; an overlapping invocation exits with status
75. The default run key is `daily-YYYY-MM-DD`, and a failed run must be retried
with a new explicit key, for example:

```sh
INVS_DAILY_RUN_KEY=daily-2026-08-13-retry-1 make daily DAILY_DATE=2026-08-13
```

Use `INVS_DAILY_SOURCE` to narrow an operator retry. `make ops-status` reports
enabled-source freshness, active runs, unresolved failed/partial runs after the
latest successful run in the last 24 hours, projection age, and data-root disk
headroom. A later successful run clears the source's unresolved alert, but older
attempts remain visible in PostgreSQL and the daily log. It exits nonzero with an
`operational_status=attention` summary when any threshold is exceeded. The
thresholds are locally configurable with `INVS_STALE_AFTER_HOURS`,
`INVS_PROJECTION_AFTER_HOURS`, and `INVS_DISK_WARN_PERCENT`.

The log is the local alert surface: inspect it for failed/partial runs, stale
source coverage, reconciliation findings, projection lag, and disk headroom.
The accepted 2026-08-13 observation below demonstrates this schedule and records
the result; implementation and documentation alone would not have checked that gate.

## Incident playbooks

### Network or provider failure

Allow the bounded transport retry policy to finish. Inspect the stage log and the
ingestion run's structured error. Do not retry authentication, schema, semantic, or
deterministic 4xx failures without a configuration/code correction. If the run is
terminal `partial` or `failed`, retry under a new run key with an explicit attempt
suffix; never turn an exhausted retry into an empty success. Reconcile before using
any accepted output.

### Provider schema or terms change

Treat the response as untrusted evidence. Preserve the raw response if the collector
did so, leave the run non-successful, and stop the affected source until its parser,
fixture, terms, and historical-fitness decision are reviewed. A current response is
not permission to reinterpret older canonical rows.

### PostgreSQL restart or migration concern

Run `make health`, then `make migrate`, then `make reconcile`. Inspect queued/running
runs before deciding whether a process is orphaned. Only an operator may cancel an
exact orphan with a reason; cancellation does not publish or delete evidence. Never
drop the application database as part of routine recovery.

### Interrupted feature or paper stage

Use the daily-cycle report to identify the last passed stage and rerun the same
specification. Successful stages are resumed, failed downstream stages are retried,
and the runner keeps the shared lock. Paper ledger event identities and database
sequence/idempotency guards prevent duplicate cash, order, fill, or approval effects;
reconcile the account before continuing. Do not edit an immutable report or ledger
event in place.

### Partial or suspicious manifest

Run `make backup-validate BACKUP_DIR=<exact-directory>` or `make reconcile` as
appropriate. Keep the affected directory quarantined and recoverable. Do not restore
from a backup with a missing, duplicate, unlisted, symlinked, or hash-mismatched file,
and do not delete an orphaned raw object to make reconciliation look clean.

### Disk pressure

Run `make ops-status` and record the filesystem reported by its disk check. At the
85% warning threshold, finish or pause nonessential research jobs and create a
validated backup. At 95%, stop new collection/derivation writes, preserve raw and
canonical evidence, and move only explicitly reviewed old backups or temporary
artifacts to a separate retention location. Never remove raw evidence or active
manifest parts to reclaim space.

### Stale data or clock/timezone issue

All cycle dates, run keys, report timestamps, and scheduler examples use UTC. Check
`date -u`, the host time service, source freshness, and projection age. A stale source
must remain visible as attention; do not advance `decision_at`, fill a missing value,
or rerun under a misleading date just to clear an alert.

### Credential or contact-data exposure

Run `make security-check`, rotate the affected provider/broker credential outside the
repository, and review retained logs/backups before sharing them. Secrets belong in
local secret storage with mode `0600` or stricter. The SEC User-Agent is different: it
must be descriptive and contain a monitored contact address, but it is not a secret
and should not be copied into API keys, manifests, or credentials.

## Capacity triggers

The current filesystem, PostgreSQL projection, and host scheduler remain the simplest
reliable v1 boundary. Revisit the design only after a measured trigger:

- Move immutable raw/artifact storage to MinIO or S3 when retained data or backup
  volume makes local disk headroom persistently unavailable, backup/restore exceeds
  the 15-minute objective, or a second host needs the same evidence. Preserve the
  existing content hashes and manifest contract during that migration.
- Introduce an orchestrator when the complete-cycle window is missed for three
  consecutive scheduled runs, or when required dependencies/backfills cannot be
  expressed by the single host runner without overlapping mutable work.
- Introduce a queue only when measured independent work requires more concurrency
  than the host can safely provide under the current lock and bounded provider rate
  limits.
- Repartition Parquet only after query/file-count measurements show a concrete scan
  bottleneck; do not repartition to hide a reconciliation or manifest problem.

### First live observation: 2026-08-13

The first real wrapper run used `DAILY_DATE=2026-08-13` and the stable key
`daily-2026-08-13` from implementation commit `68d7fd8`. The lock and continuation
behavior worked: SEC, FRED, ALFRED, and BCB completed, while Yahoo became
`partial` on a canonical natural-key conflict. The conflict was a real source
correction: the 2026-08-12 bar changed open/volume between two preserved Yahoo
response objects. The run therefore remained an attention result.

Reconciliation also surfaced one obsolete FRED snapshot part left by the earlier
full-snapshot publication behavior. After verifying its source run was terminal
and that no feature artifact referenced it, the exact content-named part was moved
to the recoverable local review area under `.runtime/unlisted-review/`; it was not
deleted. `make reconcile` then returned `issues=0`. The append-only publication fix
landed in `f92d5a5` so future new rows stay listed without unlisting prior lineage.

`make ops-status` still correctly returned `operational_status=attention` because
the source failures and partials had not yet been superseded by a successful run;
source freshness, projection age, and disk headroom were within thresholds. This
is recorded observation evidence, not a passed unattended-run gate. A clean
observation after the Yahoo correction is reviewed remains pending.

### Clean observation after correction: 2026-08-13

After reviewing the preserved Yahoo response correction and archiving the superseded
canonical/feature outputs, the exact implementation boundary `63d479d` ran:

```sh
INVS_DAILY_RUN_KEY=daily-2026-08-13-yahoo-correction-2 \
  make daily DAILY_DATE=2026-08-13
```

SEC, Yahoo, FRED, ALFRED, and BCB all succeeded. The wrapper reported
`collection_status=0`, `reconcile_status=0`, `operational_status=ok`, and
`daily_status=ok`. The source status check showed zero unresolved failed/partial
runs after each source's latest successful run; older attempts remain visible in
PostgreSQL and the daily log. The corrected Yahoo canonical partition contains 1,661
rows, and the active AAPL 2026-08-12 row has `open=305.10` and `volume=40588500`.

An exact-key retry completed successfully with zero changed canonical rows. This
observation, the bounded current-code CVM IPE replay, and the clean-root recovery
drill are recorded in the [v0.1 acceptance report](acceptance/2026-08-13-v0.1-foundation.md).

## Recovery evidence

At implementation commit `0f73e39`, the following initial drill passed on the local
Compose stack:

- backup created a new directory containing 41 immutable files and a PostgreSQL
  dump;
- the files were restored to a previously absent temporary root;
- PostgreSQL was restored into a new `restore_*` database;
- `cmd/reconcile` ran against both restored roots and returned zero findings;
- the original application database and checkout data were not overwritten.

This proved the recovery slice. The post-correction v0.1 acceptance repeated the
backup/restore path with 97 immutable files, restored database
`restore_v01_20260813`, current feature lineage, and zero reconciliation findings;
its full evidence is in the [v0.1 acceptance report](acceptance/2026-08-13-v0.1-foundation.md).
